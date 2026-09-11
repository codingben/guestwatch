package agent

import (
	"context"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"kubevirt.io/client-go/kubecli"

	"github.com/codingben/kubevirt-ai-agent/internal/domain"
)

type VMIClient interface {
	VirtualMachineInstance(namespace string) kubecli.VirtualMachineInstanceInterface
}

type NamespaceFailure struct {
	Namespace string
	Stage     domain.Stage
	Code      domain.ErrorCode
}

type DiscoveryResult struct {
	Targets  []domain.Target
	Failures []NamespaceFailure
}

func ListTargets(ctx context.Context, client VMIClient, namespaces []string, selector labels.Selector) DiscoveryResult {
	var result DiscoveryResult

	for _, ns := range namespaces {
		var targets []domain.Target
		var discoveryPageLimit int64 = 500
		var continueToken string

		for {
			opts := metav1.ListOptions{
				Limit:    discoveryPageLimit,
				Continue: continueToken,
			}
			if selector != nil {
				opts.LabelSelector = selector.String()
			}

			list, err := client.VirtualMachineInstance(ns).List(ctx, opts)
			if err != nil {
				result.Failures = append(result.Failures, NamespaceFailure{
					Namespace: ns,
					Stage:     domain.StageDiscovery,
					Code:      classifyListError(ctx, err),
				})
				targets = nil
				break
			}

			for i := range list.Items {
				vmi := &list.Items[i]
				if !domain.Eligible(vmi) {
					continue
				}
				targets = append(targets, domain.Target{
					Namespace: vmi.Namespace,
					Name:      vmi.Name,
					Node:      vmi.Status.NodeName,
					UID:       vmi.UID,
				})
			}

			if continueToken = list.Continue; continueToken == "" {
				break
			}
		}
		result.Targets = append(result.Targets, targets...)
	}

	return result
}

func classifyListError(ctx context.Context, err error) domain.ErrorCode {
	if k8serrors.IsResourceExpired(err) {
		return domain.ErrListExpired
	}
	if k8serrors.IsForbidden(err) {
		return domain.ErrPermission
	}
	if ctx.Err() != nil || k8serrors.IsTimeout(err) {
		return domain.ErrTimeout
	}
	return domain.ErrUnavailable
}
