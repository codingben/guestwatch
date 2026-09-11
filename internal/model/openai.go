package model

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"golang.org/x/time/rate"

	"github.com/codingben/kubevirt-ai-agent/internal/domain"
)

const (
	PromptVersion    = "classifier-v1"
	classifierPrompt = `You are inspecting one screenshot captured from the graphical console framebuffer of a single virtual machine. Decide whether the screenshot shows visible evidence that the guest operating system has crashed.

Respond with exactly one classification and exactly one matching reason code:

- NO_TARGET_FAILURE_VISIBLE with reason NO_FAILURE_VISIBLE: the guest appears to be running normally, at a login prompt, desktop, or any other non-crash screen.
- NO_TARGET_FAILURE_VISIBLE with reason FIRMWARE_INSTALLER_RECOVERY: the screen shows firmware (BIOS/UEFI), a bootloader, an OS installer, or a recovery console. These are not crashes.
- SUSPECTED_KERNEL_PANIC with reason KERNEL_PANIC_VISIBLE: the screen shows a Linux kernel panic (e.g. "Kernel panic", an oops trace, or an equivalent fatal Linux crash screen).
- SUSPECTED_WINDOWS_BSOD with reason WINDOWS_BSOD_VISIBLE: the screen shows a Windows "Blue Screen of Death" stop-error screen.
- UNKNOWN with reason BLANK_OR_UNREADABLE: the screen is blank, black, or otherwise carries no readable content.
- UNKNOWN with reason AMBIGUOUS_OR_CROPPED: the screen is partially visible, cropped, or otherwise ambiguous.
- UNKNOWN with reason TEXT_TOO_SMALL: text is present but too small to read reliably even at high detail.

Do not guess. If the screen does not clearly match a crash signature, classify it as NO_TARGET_FAILURE_VISIBLE or UNKNOWN rather than a suspected crash. Output only the two required fields.`
)

const (
	maxOutputTokens       = 1024
	defaultRequestTimeout = 20 * time.Second
)

type OpenAIConfig struct {
	ClassifierModel    string
	RequestsPerSecond  float64
	ConcurrentRequests int
}

type OpenAI struct {
	client  openai.Client
	model   string
	limiter *rate.Limiter
	sem     chan struct{}
	timeout time.Duration
}

func NewOpenAI(cfg OpenAIConfig, client openai.Client) (*OpenAI, error) {
	if cfg.ClassifierModel == "" {
		return nil, fmt.Errorf("model: classifier model ID must not be empty")
	}
	if cfg.RequestsPerSecond <= 0 || cfg.ConcurrentRequests <= 0 {
		return nil, fmt.Errorf("model: requests-per-second and concurrent-requests must both be positive")
	}

	return &OpenAI{
		client:  client,
		model:   cfg.ClassifierModel,
		limiter: rate.NewLimiter(rate.Limit(cfg.RequestsPerSecond), 1),
		sem:     make(chan struct{}, cfg.ConcurrentRequests),
		timeout: defaultRequestTimeout,
	}, nil
}

func (o *OpenAI) Classify(ctx context.Context, req domain.ClassifyRequest) (domain.ClassificationResult, domain.Usage, error) {
	select {
	case o.sem <- struct{}{}:
	case <-ctx.Done():
		return domain.ClassificationResult{}, domain.Usage{}, ctx.Err()
	}
	defer func() { <-o.sem }()

	if err := o.limiter.Wait(ctx); err != nil {
		return domain.ClassificationResult{}, domain.Usage{}, fmt.Errorf("model: rate limiter: %w", err)
	}
	return o.classifyOnce(ctx, req)
}

func (o *OpenAI) classifyOnce(ctx context.Context, req domain.ClassifyRequest) (domain.ClassificationResult, domain.Usage, error) {
	callCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(req.Screenshot.PNG)
	imagePart := responses.ResponseInputImageParam{
		Detail:   responses.ResponseInputImageDetailLow,
		ImageURL: param.NewOpt(imageURL),
	}
	content := responses.ResponseInputMessageContentListParam{
		responses.ResponseInputContentParamOfInputText(classifierPrompt),
		responses.ResponseInputContentUnionParam{OfInputImage: &imagePart},
	}

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(o.model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleUser),
			},
		},
		MaxOutputTokens: param.NewOpt(int64(maxOutputTokens)),
		Store:           param.NewOpt(false),
		Reasoning: shared.ReasoningParam{
			Effort: shared.ReasoningEffortNone,
		},
		Text: responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:   "classification_result",
					Schema: classificationSchema,
					Strict: param.NewOpt(true),
				},
			},
		},
	}

	resp, err := o.client.Responses.New(callCtx, params)
	if err != nil {
		return domain.ClassificationResult{}, domain.Usage{}, fmt.Errorf("model: classify request: %w", err)
	}

	usage := domain.Usage{
		InputTokens:     resp.Usage.InputTokens,
		OutputTokens:    resp.Usage.OutputTokens,
		ReasoningTokens: resp.Usage.OutputTokensDetails.ReasoningTokens,
	}

	var result domain.ClassificationResult
	if err := json.Unmarshal([]byte(resp.OutputText()), &result); err != nil {
		return domain.ClassificationResult{}, usage, fmt.Errorf("model: malformed response: %w", err)
	}

	if !ValidPair(result.Classification, result.ReasonCode) {
		return domain.ClassificationResult{}, usage, fmt.Errorf("model: malformed response: invalid classification/reason pair %q/%q", result.Classification, result.ReasonCode)
	}
	return result, usage, nil
}
