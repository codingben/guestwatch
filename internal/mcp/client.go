package mcp

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png"
	"strings"
	"time"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/codingben/guestwatch/internal/domain"
)

const (
	screenshotToolName     = string(domain.ToolConsoleScreenshot)
	consoleLogToolName     = string(domain.ToolConsoleLog)
	consoleCaptureToolName = string(domain.ToolConsoleCapture)
)

const (
	DefaultMaxImageBytes  int64 = 4 * 1024 * 1024
	DefaultMaxImagePixels int64 = 1920 * 1080 * 4
	DefaultMaxTextBytes   int64 = 64 * 1024
)

const (
	minTailLines     = 1
	maxTailLines     = 5000
	defaultTailLines = 500

	minCaptureSeconds     = 1
	maxCaptureSeconds     = 30
	defaultCaptureSeconds = 5
)

type IdentityReader interface {
	GetVMI(ctx context.Context, namespace, name string) (*kubevirtv1.VirtualMachineInstance, error)
}

type ClientConfig struct {
	ConsoleURL     string
	MaxImageBytes  int64
	MaxImagePixels int64
	MaxTextBytes   int64
	WakeScreen     bool
	TriageEnabled  bool
}

type Client struct {
	session        *mcpsdk.ClientSession
	identity       IdentityReader
	maxImageBytes  int64
	maxImagePixels int64
	maxTextBytes   int64
	wakeScreen     bool
	triageEnabled  bool
}

func NewClient(ctx context.Context, cfg ClientConfig, identity IdentityReader) (*Client, error) {
	impl := &mcpsdk.Implementation{Name: "guestwatch", Version: "0.1.0"}
	sdkClient := mcpsdk.NewClient(impl, nil)
	transport := &mcpsdk.StreamableClientTransport{Endpoint: cfg.ConsoleURL}

	session, err := sdkClient.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect to console MCP: %w", err)
	}

	c := newClientFromSession(session, identity, cfg)
	if err := c.ValidateCapabilities(ctx); err != nil {
		session.Close()
		return nil, err
	}
	return c, nil
}

func newClientFromSession(session *mcpsdk.ClientSession, identity IdentityReader, cfg ClientConfig) *Client {
	return &Client{
		session:        session,
		identity:       identity,
		maxImageBytes:  cfg.MaxImageBytes,
		maxImagePixels: cfg.MaxImagePixels,
		maxTextBytes:   cfg.MaxTextBytes,
		wakeScreen:     cfg.WakeScreen,
		triageEnabled:  cfg.TriageEnabled,
	}
}

func (c *Client) ValidateCapabilities(ctx context.Context) error {
	result, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("mcp: list tools: %w", err)
	}

	available := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		available[tool.Name] = true
	}

	required := []string{screenshotToolName}
	if c.triageEnabled {
		required = append(required, consoleLogToolName, consoleCaptureToolName)
	}

	var missing []string
	for _, name := range required {
		if !available[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("mcp: required tool(s) not found: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (c *Client) Screenshot(ctx context.Context, target domain.Target) (domain.Screenshot, error) {
	if err := c.verifyIdentity(ctx, target); err != nil {
		return domain.Screenshot{}, err
	}

	args := map[string]any{
		"namespace":   target.Namespace,
		"name":        target.Name,
		"wake_screen": c.wakeScreen,
	}
	result, err := c.callTool(ctx, screenshotToolName, args)
	if err != nil {
		return domain.Screenshot{}, err
	}

	png, err := extractImage(result)
	if err != nil {
		return domain.Screenshot{}, &domain.TargetError{Code: domain.ErrMalformedResponse, Msg: err.Error()}
	}
	if err := c.checkEvidenceLimits(png); err != nil {
		return domain.Screenshot{}, err
	}

	return domain.Screenshot{PNG: png, CapturedAt: time.Now()}, nil
}

func (c *Client) ConsoleLog(ctx context.Context, target domain.Target, tailLines int, previous bool) (string, error) {
	if err := c.verifyIdentity(ctx, target); err != nil {
		return "", err
	}

	args := map[string]any{
		"namespace":  target.Namespace,
		"name":       target.Name,
		"tail_lines": clampInt(tailLines, minTailLines, maxTailLines, defaultTailLines),
		"previous":   previous,
	}
	return c.callTextTool(ctx, consoleLogToolName, args)
}

func (c *Client) ConsoleCapture(ctx context.Context, target domain.Target, durationSeconds int) (string, error) {
	if err := c.verifyIdentity(ctx, target); err != nil {
		return "", err
	}

	args := map[string]any{
		"namespace":        target.Namespace,
		"name":             target.Name,
		"duration_seconds": clampInt(durationSeconds, minCaptureSeconds, maxCaptureSeconds, defaultCaptureSeconds),
	}
	return c.callTextTool(ctx, consoleCaptureToolName, args)
}

func clampInt(v, min, max, defaultVal int) int {
	if v <= 0 {
		return defaultVal
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func (c *Client) verifyIdentity(ctx context.Context, target domain.Target) error {
	vmi, err := c.identity.GetVMI(ctx, target.Namespace, target.Name)
	if err != nil {
		return &domain.TargetError{Code: domain.ErrPermission, Msg: fmt.Sprintf("mcp: get vmi: %v", err)}
	}
	if vmi.UID != target.UID {
		return &domain.TargetError{Code: domain.ErrStaleTarget, Msg: "mcp: vmi uid changed since discovery"}
	}
	if !domain.Eligible(vmi) {
		return &domain.TargetError{Code: domain.ErrStaleTarget, Msg: "mcp: vmi no longer eligible"}
	}
	return nil
}

func (c *Client) callTool(ctx context.Context, name string, args map[string]any) (*mcpsdk.CallToolResult, error) {
	result, err := c.session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		if ctx.Err() != nil {
			return nil, &domain.TargetError{Code: domain.ErrTimeout, Msg: fmt.Sprintf("mcp: %s: %v", name, err)}
		}
		return nil, &domain.TargetError{Code: domain.ErrUnavailable, Msg: fmt.Sprintf("mcp: %s: %v", name, err)}
	}
	if result.IsError {
		return nil, &domain.TargetError{Code: domain.ErrUnavailable, Msg: fmt.Sprintf("mcp: %s returned an error result", name)}
	}
	return result, nil
}

func (c *Client) callTextTool(ctx context.Context, name string, args map[string]any) (string, error) {
	result, err := c.session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		if ctx.Err() != nil {
			return "", &domain.TargetError{Code: domain.ErrTimeout, Msg: fmt.Sprintf("mcp: %s: %v", name, err)}
		}
		return "", &domain.TargetError{Code: domain.ErrUnavailable, Msg: fmt.Sprintf("mcp: %s: %v", name, err)}
	}

	text, err := extractText(result)
	if err != nil {
		return "", &domain.TargetError{Code: domain.ErrMalformedResponse, Msg: fmt.Sprintf("mcp: %s: %v", name, err)}
	}
	return c.boundText(text), nil
}

func (c *Client) boundText(text string) string {
	if c.maxTextBytes <= 0 || int64(len(text)) <= c.maxTextBytes {
		return text
	}
	return truncateUTF8(text, c.maxTextBytes) + "\n[truncated by guestwatch after exceeding the configured text limit]"
}

func truncateUTF8(s string, max int64) string {
	b := s[:max]
	for len(b) > 0 {
		r, size := utf8.DecodeLastRuneInString(b)
		if r != utf8.RuneError || size > 1 {
			break
		}
		b = b[:len(b)-size]
	}
	return b
}

func (c *Client) checkEvidenceLimits(png []byte) error {
	if c.maxImageBytes > 0 && int64(len(png)) > c.maxImageBytes {
		return &domain.TargetError{Code: domain.ErrEvidenceLimit, Msg: "mcp: screenshot exceeds max image bytes"}
	}
	if c.maxImagePixels > 0 {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(png))
		if err == nil && int64(cfg.Width)*int64(cfg.Height) > c.maxImagePixels {
			return &domain.TargetError{Code: domain.ErrEvidenceLimit, Msg: "mcp: screenshot exceeds max image pixels"}
		}
	}
	return nil
}

func extractImage(result *mcpsdk.CallToolResult) ([]byte, error) {
	for _, item := range result.Content {
		if img, ok := item.(*mcpsdk.ImageContent); ok {
			return img.Data, nil
		}
	}
	return nil, fmt.Errorf("mcp: console_screenshot returned no image content")
}

func extractText(result *mcpsdk.CallToolResult) (string, error) {
	var buf strings.Builder
	for _, item := range result.Content {
		if t, ok := item.(*mcpsdk.TextContent); ok {
			buf.WriteString(t.Text)
		}
	}
	if buf.Len() == 0 {
		return "", fmt.Errorf("returned no text content")
	}
	return buf.String(), nil
}
