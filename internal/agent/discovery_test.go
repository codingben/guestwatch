package agent_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/codingben/kubevirt-ai-agent/internal/agent"
	"github.com/codingben/kubevirt-ai-agent/internal/domain"
)

var _ = Describe("agent.ListTargets", func() {
	It("filters to eligible VMIs", func() {
		vmis := []kubevirtv1.VirtualMachineInstance{
			eligibleVMI("ns-a", "b-vmi", "node-1", "uid-b"),
			eligibleVMI("ns-a", "a-vmi", "node-1", "uid-a"),
		}
		paused := eligibleVMI("ns-a", "c-vmi", "node-1", "uid-c")
		paused.Status.Conditions = []kubevirtv1.VirtualMachineInstanceCondition{
			{Type: kubevirtv1.VirtualMachineInstancePaused, Status: "True"},
		}
		vmis = append(vmis, paused)

		client := &fakeVMIClient{byNamespace: map[string]*fakeVMIInterface{
			"ns-a": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				return &kubevirtv1.VirtualMachineInstanceList{Items: vmis}, nil
			}},
		}}

		result := agent.ListTargets(context.Background(), client, []string{"ns-a"}, labels.Everything())
		Expect(result.Targets).To(HaveLen(2))
		names := []string{result.Targets[0].Name, result.Targets[1].Name}
		Expect(names).To(ConsistOf("a-vmi", "b-vmi"))
	})

	It("reports an ineligible VMI as live so its dashboard entry is not pruned", func() {
		client := &fakeVMIClient{byNamespace: map[string]*fakeVMIInterface{
			"ns-a": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				paused := eligibleVMI("ns-a", "c-vmi", "node-1", "uid-c")
				paused.Status.Conditions = []kubevirtv1.VirtualMachineInstanceCondition{
					{Type: kubevirtv1.VirtualMachineInstancePaused, Status: "True"},
				}
				return &kubevirtv1.VirtualMachineInstanceList{Items: []kubevirtv1.VirtualMachineInstance{paused}}, nil
			}},
		}}

		result := agent.ListTargets(context.Background(), client, []string{"ns-a"}, labels.Everything())
		Expect(result.Targets).To(BeEmpty(), "the paused VMI is ineligible for capture")
		Expect(result.Live).To(HaveKey("ns-a/c-vmi"), "but it still exists, so it must not be pruned from the dashboard")
	})

	It("lists namespaces fully populate ListedNamespaces and Live", func() {
		client := &fakeVMIClient{byNamespace: map[string]*fakeVMIInterface{
			"ns-a": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				return &kubevirtv1.VirtualMachineInstanceList{
					Items: []kubevirtv1.VirtualMachineInstance{eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1")},
				}, nil
			}},
			"ns-b": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				return &kubevirtv1.VirtualMachineInstanceList{
					Items: []kubevirtv1.VirtualMachineInstance{eligibleVMI("ns-b", "vmi-2", "node-2", "uid-2")},
				}, nil
			}},
		}}

		result := agent.ListTargets(context.Background(), client, []string{"ns-a", "ns-b"}, labels.Everything())
		Expect(result.ListedNamespaces).To(ConsistOf("ns-a", "ns-b"))
		Expect(result.Live).To(HaveKey("ns-a/vmi-1"))
		Expect(result.Live).To(HaveKey("ns-b/vmi-2"))
	})

	It("paginates using the continue token", func() {
		page1 := &kubevirtv1.VirtualMachineInstanceList{
			ListMeta: metav1.ListMeta{Continue: "page-2"},
			Items:    []kubevirtv1.VirtualMachineInstance{eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1")},
		}
		page2 := &kubevirtv1.VirtualMachineInstanceList{
			Items: []kubevirtv1.VirtualMachineInstance{eligibleVMI("ns-a", "vmi-2", "node-1", "uid-2")},
		}
		var calls int
		client := &fakeVMIClient{byNamespace: map[string]*fakeVMIInterface{
			"ns-a": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				calls++
				if opts.Continue == "" {
					return page1, nil
				}
				Expect(opts.Continue).To(Equal("page-2"))
				return page2, nil
			}},
		}}

		result := agent.ListTargets(context.Background(), client, []string{"ns-a"}, labels.Everything())
		Expect(calls).To(Equal(2))
		Expect(result.Targets).To(HaveLen(2))
	})

	It("records a namespace failure on a 410 Gone and continues other namespaces; the next scan retries", func() {
		client := &fakeVMIClient{byNamespace: map[string]*fakeVMIInterface{
			"ns-a": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				return nil, k8serrors.NewResourceExpired("continue token expired")
			}},
			"ns-b": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				return &kubevirtv1.VirtualMachineInstanceList{
					Items: []kubevirtv1.VirtualMachineInstance{eligibleVMI("ns-b", "vmi-1", "node-1", "uid-1")},
				}, nil
			}},
		}}

		result := agent.ListTargets(context.Background(), client, []string{"ns-a", "ns-b"}, labels.Everything())
		Expect(result.Failures).To(HaveLen(1))
		Expect(result.Failures[0].Namespace).To(Equal("ns-a"))
		Expect(result.Failures[0].Code).To(Equal(domain.ErrListExpired))
		Expect(result.Targets).To(HaveLen(1))
		Expect(result.Targets[0].Namespace).To(Equal("ns-b"))
		Expect(result.ListedNamespaces).To(ConsistOf("ns-b"), "ns-a failed to list and must be excluded, or pruning would wrongly evict its entries")
	})

	It("isolates a permission failure in one namespace: outer namespaces are unaffected (C1)", func() {
		client := &fakeVMIClient{byNamespace: map[string]*fakeVMIInterface{
			"ns-a": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				return &kubevirtv1.VirtualMachineInstanceList{
					Items: []kubevirtv1.VirtualMachineInstance{eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1")},
				}, nil
			}},
			"ns-b": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				return nil, k8serrors.NewForbidden(schema.GroupResource{Resource: "virtualmachineinstances"}, "", errors.New("no rbac"))
			}},
			"ns-c": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				return &kubevirtv1.VirtualMachineInstanceList{
					Items: []kubevirtv1.VirtualMachineInstance{eligibleVMI("ns-c", "vmi-1", "node-1", "uid-2")},
				}, nil
			}},
		}}

		result := agent.ListTargets(context.Background(), client, []string{"ns-a", "ns-b", "ns-c"}, labels.Everything())
		Expect(result.Failures).To(HaveLen(1))
		Expect(result.Failures[0].Namespace).To(Equal("ns-b"))
		Expect(result.Failures[0].Code).To(Equal(domain.ErrPermission))
		Expect(result.Targets).To(HaveLen(2), "ns-a and ns-c must still be discovered despite ns-b's failure")
	})
})
