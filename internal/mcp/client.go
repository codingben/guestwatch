package mcp

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/codingben/kubevirt-ai-agent/internal/domain"
)

const screenshotToolName = "console_screenshot"

const (
	DefaultMaxImageBytes  int64 = 8 * 1024 * 1024
	DefaultMaxImagePixels int64 = 1920 * 1080 * 4
)

type IdentityReader interface {
	GetVMI(ctx context.Context, namespace, name string) (*kubevirtv1.VirtualMachineInstance, error)
}

type ClientConfig struct {
	ConsoleURL     string
	MaxImageBytes  int64
	MaxImagePixels int64
}

type Client struct {
	session        *mcpsdk.ClientSession
	identity       IdentityReader
	maxImageBytes  int64
	maxImagePixels int64
}

func NewClient(ctx context.Context, cfg ClientConfig, identity IdentityReader) (*Client, error) {
	impl := &mcpsdk.Implementation{Name: "kubevirt-ai-agent", Version: "0.1.0"}
	sdkClient := mcpsdk.NewClient(impl, nil)
	transport := &mcpsdk.StreamableClientTransport{Endpoint: cfg.ConsoleURL}

	session, err := sdkClient.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect to console MCP: %w", err)
	}

	c := newClientFromSession(session, identity, cfg.MaxImageBytes, cfg.MaxImagePixels)
	if err := c.ValidateCapabilities(ctx); err != nil {
		session.Close()
		return nil, err
	}
	return c, nil
}

func newClientFromSession(session *mcpsdk.ClientSession, identity IdentityReader, maxImageBytes, maxImagePixels int64) *Client {
	return &Client{
		session:        session,
		identity:       identity,
		maxImageBytes:  maxImageBytes,
		maxImagePixels: maxImagePixels,
	}
}

func (c *Client) ValidateCapabilities(ctx context.Context) error {
	result, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("mcp: list tools: %w", err)
	}

	for _, tool := range result.Tools {
		if tool.Name != screenshotToolName {
			continue
		}
		if !schemaAcceptsExpectedUID(tool.InputSchema) {
			return fmt.Errorf("mcp: %s does not accept expected_uid", screenshotToolName)
		}
		return nil
	}
	return fmt.Errorf("mcp: %s tool not found", screenshotToolName)
}

func schemaAcceptsExpectedUID(schema any) bool {
	obj, ok := schema.(map[string]any)
	if !ok {
		return false
	}
	props, ok := obj["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = props["expected_uid"]
	return ok
}

func (c *Client) Screenshot(ctx context.Context, target domain.Target) (domain.Screenshot, error) {
	if err := c.verifyIdentity(ctx, target); err != nil {
		return domain.Screenshot{}, err
	}

	result, err := c.callScreenshotTool(ctx, target)
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

func (c *Client) callScreenshotTool(ctx context.Context, target domain.Target) (*mcpsdk.CallToolResult, error) {
	args := map[string]any{
		"namespace":    target.Namespace,
		"name":         target.Name,
		"expected_uid": string(target.UID),
	}

	result, err := c.session.CallTool(ctx, &mcpsdk.CallToolParams{Name: screenshotToolName, Arguments: args})
	if err != nil {
		if ctx.Err() != nil {
			return nil, &domain.TargetError{Code: domain.ErrTimeout, Msg: fmt.Sprintf("mcp: screenshot: %v", err)}
		}
		return nil, &domain.TargetError{Code: domain.ErrUnavailable, Msg: fmt.Sprintf("mcp: screenshot: %v", err)}
	}
	if result.IsError {
		return nil, &domain.TargetError{Code: domain.ErrUnavailable, Msg: "mcp: console_screenshot returned an error result"}
	}
	return result, nil
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
