package mcp

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/codingben/kubevirt-ai-agent/internal/domain"
)

type screenshotArgs struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	WakeScreen bool   `json:"wake_screen,omitempty"`
}

// screenshotBehavior lets each test control what the fake console_screenshot
// tool returns.
type screenshotBehavior func(args screenshotArgs) (*mcpsdk.CallToolResult, error)

func fakePNG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	Expect(png.Encode(&buf, img)).To(Succeed())
	return buf.Bytes()
}

// startFakeConsoleMCP starts an in-memory MCP server exposing a
// console_screenshot tool (or none, if includeTool is false) and returns a
// connected client session.
func startFakeConsoleMCP(includeTool bool, behavior screenshotBehavior) *mcpsdk.ClientSession {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "fake-console-mcp", Version: "0.0.0"}, nil)
	if includeTool {
		mcpsdk.AddTool(server, &mcpsdk.Tool{
			Name:        "console_screenshot",
			Description: "capture a screenshot",
		}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args screenshotArgs) (*mcpsdk.CallToolResult, any, error) {
			result, err := behavior(args)
			return result, nil, err
		})
	}

	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	_, err := server.Connect(context.Background(), serverTransport, nil)
	Expect(err).NotTo(HaveOccurred())

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	Expect(err).NotTo(HaveOccurred())
	return session
}

func imageResult(png []byte) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.ImageContent{Data: png, MIMEType: "image/png"}},
	}
}

type fakeIdentityReader struct {
	vmi *kubevirtv1.VirtualMachineInstance
	err error
}

func (f fakeIdentityReader) GetVMI(ctx context.Context, namespace, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	return f.vmi, f.err
}

func eligibleVMI(uid types.UID, node string) *kubevirtv1.VirtualMachineInstance {
	return &kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{UID: uid, Namespace: "payments", Name: "checkout-7"},
		Status: kubevirtv1.VirtualMachineInstanceStatus{
			Phase:    kubevirtv1.Running,
			NodeName: node,
		},
	}
}

func sampleTarget(uid types.UID) domain.Target {
	return domain.Target{Namespace: "payments", Name: "checkout-7", Node: "node-1", UID: uid}
}

var _ = Describe("Client.ValidateCapabilities", func() {
	It("succeeds when console_screenshot is present", func() {
		session := startFakeConsoleMCP(true, func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
			return imageResult(fakePNG(2, 2)), nil
		})
		defer session.Close()

		c := newClientFromSession(session, fakeIdentityReader{}, 0, 0)
		Expect(c.ValidateCapabilities(context.Background())).To(Succeed())
	})

	It("fails when console_screenshot is not offered", func() {
		session := startFakeConsoleMCP(false, nil)
		defer session.Close()

		c := newClientFromSession(session, fakeIdentityReader{}, 0, 0)
		Expect(c.ValidateCapabilities(context.Background())).To(HaveOccurred())
	})
})

var _ = Describe("Client.Screenshot", func() {
	const uid = types.UID("abc-123")

	It("re-validates identity before capture and rejects a UID mismatch as stale", func() {
		c := newClientFromSession(nil, fakeIdentityReader{vmi: eligibleVMI("different-uid", "node-1")}, 0, 0)
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		Expect(err).To(HaveOccurred())
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrStaleTarget))
	})

	It("rejects a target that is no longer eligible", func() {
		vmi := eligibleVMI(uid, "node-1")
		vmi.Status.Phase = kubevirtv1.Succeeded
		c := newClientFromSession(nil, fakeIdentityReader{vmi: vmi}, 0, 0)
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrStaleTarget))
	})

	It("surfaces a GetVMI failure as a permission error", func() {
		c := newClientFromSession(nil, fakeIdentityReader{err: errors.New("forbidden")}, 0, 0)
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrPermission))
	})

	It("captures exactly once and returns the PNG when identity and capture succeed", func() {
		var calls int
		session := startFakeConsoleMCP(true, func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
			calls++
			Expect(args.Namespace).To(Equal("payments"))
			Expect(args.Name).To(Equal("checkout-7"))
			Expect(args.WakeScreen).To(BeTrue())
			return imageResult(fakePNG(2, 2)), nil
		})
		defer session.Close()

		c := newClientFromSession(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, 0, 0)
		shot, err := c.Screenshot(context.Background(), sampleTarget(uid))
		Expect(err).NotTo(HaveOccurred())
		Expect(shot.PNG).NotTo(BeEmpty())
		Expect(calls).To(Equal(1))
	})

	It("treats an IsError tool result as unavailable", func() {
		session := startFakeConsoleMCP(true, func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{IsError: true}, nil
		})
		defer session.Close()

		c := newClientFromSession(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, 0, 0)
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrUnavailable))
	})

	It("rejects a result with no image content as malformed", func() {
		session := startFakeConsoleMCP(true, func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "oops"}}}, nil
		})
		defer session.Close()

		c := newClientFromSession(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, 0, 0)
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrMalformedResponse))
	})

	It("rejects a screenshot exceeding the configured byte limit", func() {
		png := fakePNG(4, 4)
		session := startFakeConsoleMCP(true, func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
			return imageResult(png), nil
		})
		defer session.Close()

		c := newClientFromSession(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, int64(len(png)-1), 0)
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrEvidenceLimit))
	})

	It("rejects a screenshot exceeding the configured pixel limit", func() {
		session := startFakeConsoleMCP(true, func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
			return imageResult(fakePNG(10, 10)), nil
		})
		defer session.Close()

		c := newClientFromSession(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, 0, 50)
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrEvidenceLimit))
	})
})
