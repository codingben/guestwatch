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

type consoleLogArgs struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	TailLines int    `json:"tail_lines,omitempty"`
	Previous  bool   `json:"previous,omitempty"`
}

type consoleCaptureArgs struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	DurationSeconds int    `json:"duration_seconds,omitempty"`
}

// screenshotBehavior lets each test control what the fake console_screenshot
// tool returns.
type screenshotBehavior func(args screenshotArgs) (*mcpsdk.CallToolResult, error)
type consoleLogBehavior func(args consoleLogArgs) (*mcpsdk.CallToolResult, error)
type consoleCaptureBehavior func(args consoleCaptureArgs) (*mcpsdk.CallToolResult, error)

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

// fakeConsoleMCPOptions controls which tools startFakeConsoleMCP exposes.
type fakeConsoleMCPOptions struct {
	screenshot         screenshotBehavior
	includeLog         bool
	log                consoleLogBehavior
	includeCapture     bool
	capture            consoleCaptureBehavior
	omitScreenshotTool bool
}

// startFakeConsoleMCP starts an in-memory MCP server exposing whichever
// tools opts requests, and returns a connected client session.
func startFakeConsoleMCP(opts fakeConsoleMCPOptions) *mcpsdk.ClientSession {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "fake-console-mcp", Version: "0.0.0"}, nil)
	if !opts.omitScreenshotTool {
		mcpsdk.AddTool(server, &mcpsdk.Tool{
			Name:        screenshotToolName,
			Description: "capture a screenshot",
		}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args screenshotArgs) (*mcpsdk.CallToolResult, any, error) {
			result, err := opts.screenshot(args)
			return result, nil, err
		})
	}
	if opts.includeLog {
		mcpsdk.AddTool(server, &mcpsdk.Tool{
			Name:        consoleLogToolName,
			Description: "read persisted console log",
		}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args consoleLogArgs) (*mcpsdk.CallToolResult, any, error) {
			result, err := opts.log(args)
			return result, nil, err
		})
	}
	if opts.includeCapture {
		mcpsdk.AddTool(server, &mcpsdk.Tool{
			Name:        consoleCaptureToolName,
			Description: "capture live console output",
		}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args consoleCaptureArgs) (*mcpsdk.CallToolResult, any, error) {
			result, err := opts.capture(args)
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

func textResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
	}
}

func errorTextResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
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

func newTestClient(session *mcpsdk.ClientSession, identity IdentityReader, cfg ClientConfig) *Client {
	return newClientFromSession(session, identity, cfg)
}

var _ = Describe("Client.ValidateCapabilities", func() {
	It("succeeds when console_screenshot is present and triage is disabled", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			screenshot: func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
				return imageResult(fakePNG(2, 2)), nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{}, ClientConfig{WakeScreen: true})
		Expect(c.ValidateCapabilities(context.Background())).To(Succeed())
	})

	It("fails when console_screenshot is not offered", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{omitScreenshotTool: true})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{}, ClientConfig{WakeScreen: true})
		Expect(c.ValidateCapabilities(context.Background())).To(HaveOccurred())
	})

	It("succeeds when triage is enabled and all three tools are present", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			screenshot:     func(args screenshotArgs) (*mcpsdk.CallToolResult, error) { return imageResult(fakePNG(2, 2)), nil },
			includeLog:     true,
			log:            func(args consoleLogArgs) (*mcpsdk.CallToolResult, error) { return textResult("log"), nil },
			includeCapture: true,
			capture:        func(args consoleCaptureArgs) (*mcpsdk.CallToolResult, error) { return textResult("capture"), nil },
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{}, ClientConfig{WakeScreen: true, TriageEnabled: true})
		Expect(c.ValidateCapabilities(context.Background())).To(Succeed())
	})

	It("fails and names the missing tool when triage is enabled but console_log is absent", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			screenshot:     func(args screenshotArgs) (*mcpsdk.CallToolResult, error) { return imageResult(fakePNG(2, 2)), nil },
			includeCapture: true,
			capture:        func(args consoleCaptureArgs) (*mcpsdk.CallToolResult, error) { return textResult("capture"), nil },
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{}, ClientConfig{WakeScreen: true, TriageEnabled: true})
		err := c.ValidateCapabilities(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(consoleLogToolName))
	})
})

var _ = Describe("Client.Screenshot", func() {
	const uid = types.UID("abc-123")

	It("re-validates identity before capture and rejects a UID mismatch as stale", func() {
		c := newTestClient(nil, fakeIdentityReader{vmi: eligibleVMI("different-uid", "node-1")}, ClientConfig{WakeScreen: true})
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		Expect(err).To(HaveOccurred())
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrStaleTarget))
	})

	It("rejects a target that is no longer eligible", func() {
		vmi := eligibleVMI(uid, "node-1")
		vmi.Status.Phase = kubevirtv1.Succeeded
		c := newTestClient(nil, fakeIdentityReader{vmi: vmi}, ClientConfig{WakeScreen: true})
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrStaleTarget))
	})

	It("surfaces a GetVMI failure as a permission error", func() {
		c := newTestClient(nil, fakeIdentityReader{err: errors.New("forbidden")}, ClientConfig{WakeScreen: true})
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrPermission))
	})

	It("captures exactly once and returns the PNG when identity and capture succeed", func() {
		var calls int
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			screenshot: func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
				calls++
				Expect(args.Namespace).To(Equal("payments"))
				Expect(args.Name).To(Equal("checkout-7"))
				Expect(args.WakeScreen).To(BeTrue())
				return imageResult(fakePNG(2, 2)), nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{WakeScreen: true})
		shot, err := c.Screenshot(context.Background(), sampleTarget(uid))
		Expect(err).NotTo(HaveOccurred())
		Expect(shot.PNG).NotTo(BeEmpty())
		Expect(calls).To(Equal(1))
	})

	It("passes wake_screen: false through when configured off", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			screenshot: func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
				Expect(args.WakeScreen).To(BeFalse())
				return imageResult(fakePNG(2, 2)), nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{WakeScreen: false})
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		Expect(err).NotTo(HaveOccurred())
	})

	It("treats an IsError tool result as unavailable", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			screenshot: func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{IsError: true}, nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{WakeScreen: true})
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrUnavailable))
	})

	It("rejects a result with no image content as malformed", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			screenshot: func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "oops"}}}, nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{WakeScreen: true})
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrMalformedResponse))
	})

	It("rejects a screenshot exceeding the configured byte limit", func() {
		png := fakePNG(4, 4)
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			screenshot: func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
				return imageResult(png), nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{WakeScreen: true, MaxImageBytes: int64(len(png) - 1)})
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrEvidenceLimit))
	})

	It("rejects a screenshot exceeding the configured pixel limit", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			screenshot: func(args screenshotArgs) (*mcpsdk.CallToolResult, error) {
				return imageResult(fakePNG(10, 10)), nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{WakeScreen: true, MaxImagePixels: 50})
		_, err := c.Screenshot(context.Background(), sampleTarget(uid))
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrEvidenceLimit))
	})
})

var _ = Describe("Client.ConsoleLog", func() {
	const uid = types.UID("abc-123")

	It("re-validates identity before reading the log", func() {
		c := newTestClient(nil, fakeIdentityReader{vmi: eligibleVMI("different-uid", "node-1")}, ClientConfig{})
		_, err := c.ConsoleLog(context.Background(), sampleTarget(uid), 0, false)
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrStaleTarget))
	})

	It("defaults tail_lines when zero and clamps out-of-range values", func() {
		var seen []int
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			includeLog: true,
			log: func(args consoleLogArgs) (*mcpsdk.CallToolResult, error) {
				seen = append(seen, args.TailLines)
				return textResult("boot ok"), nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{})
		_, err := c.ConsoleLog(context.Background(), sampleTarget(uid), 0, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = c.ConsoleLog(context.Background(), sampleTarget(uid), 999999, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = c.ConsoleLog(context.Background(), sampleTarget(uid), -5, false)
		Expect(err).NotTo(HaveOccurred())

		Expect(seen).To(Equal([]int{defaultTailLines, maxTailLines, defaultTailLines}))
	})

	It("returns a tool-level error result as text rather than failing", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			includeLog: true,
			log: func(args consoleLogArgs) (*mcpsdk.CallToolResult, error) {
				return errorTextResult("Code: SERIAL_DISABLED\nautoattachSerialConsole is false"), nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{})
		text, err := c.ConsoleLog(context.Background(), sampleTarget(uid), 0, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(ContainSubstring("SERIAL_DISABLED"))
	})

	It("truncates text exceeding the configured byte limit", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			includeLog: true,
			log: func(args consoleLogArgs) (*mcpsdk.CallToolResult, error) {
				return textResult("0123456789"), nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{MaxTextBytes: 4})
		text, err := c.ConsoleLog(context.Background(), sampleTarget(uid), 0, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(HavePrefix("0123"))
		Expect(text).To(ContainSubstring("truncated"))
	})

	It("surfaces a transport failure as unavailable", func() {
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{})
		_, err := c.ConsoleLog(context.Background(), sampleTarget(uid), 0, false)
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrUnavailable))
	})
})

var _ = Describe("Client.ConsoleCapture", func() {
	const uid = types.UID("abc-123")

	It("re-validates identity before capturing", func() {
		c := newTestClient(nil, fakeIdentityReader{vmi: eligibleVMI("different-uid", "node-1")}, ClientConfig{})
		_, err := c.ConsoleCapture(context.Background(), sampleTarget(uid), 0)
		var te *domain.TargetError
		Expect(errors.As(err, &te)).To(BeTrue())
		Expect(te.Code).To(Equal(domain.ErrStaleTarget))
	})

	It("defaults and clamps duration_seconds", func() {
		var seen []int
		session := startFakeConsoleMCP(fakeConsoleMCPOptions{
			includeCapture: true,
			capture: func(args consoleCaptureArgs) (*mcpsdk.CallToolResult, error) {
				seen = append(seen, args.DurationSeconds)
				return textResult("quiet"), nil
			},
		})
		defer session.Close()

		c := newTestClient(session, fakeIdentityReader{vmi: eligibleVMI(uid, "node-1")}, ClientConfig{})
		_, err := c.ConsoleCapture(context.Background(), sampleTarget(uid), 0)
		Expect(err).NotTo(HaveOccurred())
		_, err = c.ConsoleCapture(context.Background(), sampleTarget(uid), 999)
		Expect(err).NotTo(HaveOccurred())

		Expect(seen).To(Equal([]int{defaultCaptureSeconds, maxCaptureSeconds}))
	})
})
