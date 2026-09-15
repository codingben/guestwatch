package model_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/codingben/kubevirt-ai-agent/internal/domain"
	"github.com/codingben/kubevirt-ai-agent/internal/model"
)

func fakeResponseBody(outputText string) []byte {
	body := map[string]any{
		"id":         "resp_1",
		"object":     "response",
		"created_at": 0,
		"status":     "completed",
		"model":      "test-model",
		"output": []map[string]any{
			{
				"type":   "message",
				"id":     "msg_1",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": outputText, "annotations": []any{}},
				},
			},
		},
		"parallel_tool_calls": false,
		"tool_choice":         "auto",
		"tools":               []any{},
		"usage": map[string]any{
			"input_tokens":          100,
			"input_tokens_details":  map[string]any{"cached_tokens": 0, "cache_write_tokens": 0},
			"output_tokens":         20,
			"output_tokens_details": map[string]any{"reasoning_tokens": 5},
			"total_tokens":          120,
		},
	}
	data, _ := json.Marshal(body)
	return data
}

func newTestClassifier(handler http.HandlerFunc) (*model.OpenAI, *httptest.Server) {
	server := httptest.NewServer(handler)
	client := openai.NewClient(
		option.WithBaseURL(server.URL+"/"),
		option.WithAPIKey("test-key"),
		option.WithMaxRetries(0),
	)
	classifier, err := model.NewOpenAI(model.OpenAIConfig{
		ClassifierModel:    "test-model",
		RequestsPerSecond:  1000,
		ConcurrentRequests: 10,
	}, client)
	Expect(err).NotTo(HaveOccurred())
	return classifier, server
}

func sampleRequest() domain.ClassifyRequest {
	return domain.ClassifyRequest{
		Target:     domain.Target{Namespace: "payments", Name: "checkout-7"},
		Screenshot: domain.Screenshot{PNG: []byte{0x89, 'P', 'N', 'G'}, CapturedAt: time.Now()},
	}
}

var _ = Describe("model.ValidPair", func() {
	DescribeTable("classification/reason pairs",
		func(class domain.Classification, reason domain.ReasonCode, want bool) {
			Expect(model.ValidPair(class, reason)).To(Equal(want))
		},
		Entry("no-target + no-failure", domain.NoTargetFailureVisible, domain.NoFailureVisible, true),
		Entry("no-target + firmware", domain.NoTargetFailureVisible, domain.FirmwareInstallerRecovery, true),
		Entry("no-target + kernel panic reason is invalid", domain.NoTargetFailureVisible, domain.KernelPanicVisible, false),
		Entry("kernel panic + kernel panic visible", domain.SuspectedKernelPanic, domain.KernelPanicVisible, true),
		Entry("kernel panic + bsod reason is invalid", domain.SuspectedKernelPanic, domain.WindowsBSODVisible, false),
		Entry("bsod + bsod visible", domain.SuspectedWindowsBSOD, domain.WindowsBSODVisible, true),
		Entry("unknown + blank", domain.Unknown, domain.BlankOrUnreadable, true),
		Entry("unknown + ambiguous", domain.Unknown, domain.AmbiguousOrCropped, true),
		Entry("unknown + text too small", domain.Unknown, domain.TextTooSmall, true),
		Entry("unknown + kernel panic reason is invalid", domain.Unknown, domain.KernelPanicVisible, false),
	)
})

var _ = Describe("OpenAI.Classify", func() {
	It("sends low image detail, strict json schema, and store:false", func() {
		var captured map[string]any
		classifier, server := newTestClassifier(func(w http.ResponseWriter, r *http.Request) {
			Expect(json.NewDecoder(r.Body).Decode(&captured)).To(Succeed())
			w.Header().Set("Content-Type", "application/json")
			w.Write(fakeResponseBody(`{"classification":"NO_TARGET_FAILURE_VISIBLE","reason_code":"NO_FAILURE_VISIBLE"}`))
		})
		defer server.Close()

		result, usage, err := classifier.Classify(context.Background(), sampleRequest())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Classification).To(Equal(domain.NoTargetFailureVisible))
		Expect(usage.InputTokens).To(Equal(int64(100)))
		Expect(usage.OutputTokens).To(Equal(int64(20)))
		Expect(usage.ReasoningTokens).To(Equal(int64(5)))

		Expect(captured["store"]).To(Equal(false))
		Expect(captured["max_output_tokens"]).To(BeNumerically("==", 1024))

		text := captured["text"].(map[string]any)
		format := text["format"].(map[string]any)
		Expect(format["type"]).To(Equal("json_schema"))
		Expect(format["strict"]).To(Equal(true))

		input := captured["input"].([]any)
		message := input[0].(map[string]any)
		content := message["content"].([]any)
		imagePart := content[1].(map[string]any)
		Expect(imagePart["type"]).To(Equal("input_image"))
		Expect(imagePart["detail"]).To(Equal("low"))
	})

	It("rejects a malformed classification/reason pair as an operational failure, not a finding", func() {
		classifier, server := newTestClassifier(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(fakeResponseBody(`{"classification":"NO_TARGET_FAILURE_VISIBLE","reason_code":"KERNEL_PANIC_VISIBLE"}`))
		})
		defer server.Close()

		_, _, err := classifier.Classify(context.Background(), sampleRequest())
		Expect(err).To(HaveOccurred())
	})

	It("rejects malformed JSON output", func() {
		classifier, server := newTestClassifier(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(fakeResponseBody(`not json`))
		})
		defer server.Close()

		_, _, err := classifier.Classify(context.Background(), sampleRequest())
		Expect(err).To(HaveOccurred())
	})

	It("returns an error on a 500 in exactly one attempt; the next scan retries", func() {
		var attempts int32
		classifier, server := newTestClassifier(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
		})
		defer server.Close()

		_, _, err := classifier.Classify(context.Background(), sampleRequest())
		Expect(err).To(HaveOccurred())
		Expect(atomic.LoadInt32(&attempts)).To(Equal(int32(1)))
	})

	It("returns an error on a 400 in exactly one attempt", func() {
		var attempts int32
		classifier, server := newTestClassifier(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"bad request","type":"invalid_request_error"}}`))
		})
		defer server.Close()

		_, _, err := classifier.Classify(context.Background(), sampleRequest())
		Expect(err).To(HaveOccurred())
		Expect(atomic.LoadInt32(&attempts)).To(Equal(int32(1)))
	})

	It("bounds concurrent requests to the configured limit", func() {
		var inFlight, maxInFlight int32
		release := make(chan struct{})
		classifier, server := newTestClassifier(func(w http.ResponseWriter, r *http.Request) {
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				max := atomic.LoadInt32(&maxInFlight)
				if cur <= max || atomic.CompareAndSwapInt32(&maxInFlight, max, cur) {
					break
				}
			}
			<-release
			atomic.AddInt32(&inFlight, -1)
			w.Header().Set("Content-Type", "application/json")
			w.Write(fakeResponseBody(`{"classification":"UNKNOWN","reason_code":"BLANK_OR_UNREADABLE"}`))
		})
		defer server.Close()

		limited, err := model.NewOpenAI(model.OpenAIConfig{
			ClassifierModel:    "test-model",
			RequestsPerSecond:  1000,
			ConcurrentRequests: 2,
		}, openai.NewClient(option.WithBaseURL(server.URL+"/"), option.WithAPIKey("test-key")))
		Expect(err).NotTo(HaveOccurred())
		_ = classifier

		done := make(chan struct{})
		for i := 0; i < 5; i++ {
			go func() {
				_, _, _ = limited.Classify(context.Background(), sampleRequest())
				done <- struct{}{}
			}()
		}

		Eventually(func() int32 { return atomic.LoadInt32(&inFlight) }).Should(Equal(int32(2)))
		close(release)
		for i := 0; i < 5; i++ {
			<-done
		}
		Expect(atomic.LoadInt32(&maxInFlight)).To(Equal(int32(2)))
	})
})

var _ = Describe("model.NewOpenAI", func() {
	It("rejects an empty classifier model ID", func() {
		_, err := model.NewOpenAI(model.OpenAIConfig{}, openai.NewClient())
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("fakeResponseBody", func() {
	It("is valid JSON", func() {
		var v map[string]any
		Expect(json.Unmarshal(fakeResponseBody("x"), &v)).To(Succeed())
		Expect(fmt.Sprintf("%v", v["status"])).To(Equal("completed"))
	})
})

func fakeFunctionCallResponseBody(callID, name, arguments string) []byte {
	return fakeFunctionCallResponseBodyWithReasoning(callID, name, arguments, "")
}

// fakeFunctionCallResponseBodyWithReasoning adds a leading reasoning
// output item when reasoningID is non-empty.
func fakeFunctionCallResponseBodyWithReasoning(callID, name, arguments, reasoningID string) []byte {
	output := []map[string]any{}
	if reasoningID != "" {
		output = append(output, map[string]any{
			"type":              "reasoning",
			"id":                reasoningID,
			"summary":           []map[string]any{{"type": "summary_text", "text": "considering the console log"}},
			"encrypted_content": "encrypted-blob",
			"status":            "completed",
		})
	}
	output = append(output, map[string]any{
		"type":      "function_call",
		"id":        "fc_1",
		"call_id":   callID,
		"name":      name,
		"arguments": arguments,
		"status":    "completed",
	})

	body := map[string]any{
		"id":                  "resp_fc",
		"object":              "response",
		"created_at":          0,
		"status":              "completed",
		"model":               "test-triage-model",
		"output":              output,
		"parallel_tool_calls": false,
		"tool_choice":         "auto",
		"tools":               []any{},
		"usage": map[string]any{
			"input_tokens":          50,
			"input_tokens_details":  map[string]any{"cached_tokens": 0, "cache_write_tokens": 0},
			"output_tokens":         10,
			"output_tokens_details": map[string]any{"reasoning_tokens": 2},
			"total_tokens":          60,
		},
	}
	data, _ := json.Marshal(body)
	return data
}

// fakeIncompleteResponseBody mimics a response cut off by max_output_tokens.
func fakeIncompleteResponseBody() []byte {
	body := map[string]any{
		"id":                  "resp_incomplete",
		"object":              "response",
		"created_at":          0,
		"status":              "incomplete",
		"incomplete_details":  map[string]any{"reason": "max_output_tokens"},
		"model":               "test-triage-model",
		"output":              []map[string]any{},
		"parallel_tool_calls": false,
		"tool_choice":         "auto",
		"tools":               []any{},
		"usage": map[string]any{
			"input_tokens":          50,
			"input_tokens_details":  map[string]any{"cached_tokens": 0, "cache_write_tokens": 0},
			"output_tokens":         2048,
			"output_tokens_details": map[string]any{"reasoning_tokens": 2048},
			"total_tokens":          2098,
		},
	}
	data, _ := json.Marshal(body)
	return data
}

const validTriageJSON = `{"suspectedCause":"KERNEL_PANIC","confidence":"HIGH","summary":"kernel panic visible","keyEvidence":["oops trace"],"nextSteps":["reboot the VM"]}`

func newTestTriageModel(handler http.HandlerFunc) (*model.OpenAI, *httptest.Server) {
	server := httptest.NewServer(handler)
	client := openai.NewClient(
		option.WithBaseURL(server.URL+"/"),
		option.WithAPIKey("test-key"),
		option.WithMaxRetries(0),
	)
	m, err := model.NewOpenAI(model.OpenAIConfig{
		ClassifierModel:          "test-model",
		RequestsPerSecond:        1000,
		ConcurrentRequests:       10,
		TriageModel:              "test-triage-model",
		TriageRequestsPerSecond:  1000,
		TriageConcurrentRequests: 10,
		TriageMaxToolCalls:       5,
		TriageTimeout:            5 * time.Second,
	}, client)
	Expect(err).NotTo(HaveOccurred())
	return m, server
}

func sampleTriageRequest() domain.TriageRequest {
	return domain.TriageRequest{
		Target:             domain.Target{Namespace: "payments", Name: "checkout-7", Node: "node-1"},
		Classification:     domain.SuspectedKernelPanic,
		ReasonCode:         domain.KernelPanicVisible,
		ClassificationTime: time.Now(),
	}
}

// fakeToolRunner is a model.ToolRunner whose methods are individually
// overridable and record which tools were invoked.
type fakeToolRunner struct {
	screenshotFunc     func(ctx context.Context, target domain.Target) (domain.Screenshot, error)
	consoleLogFunc     func(ctx context.Context, target domain.Target, tailLines int, previous bool) (string, error)
	consoleCaptureFunc func(ctx context.Context, target domain.Target, durationSeconds int) (string, error)
	calls              []string
}

func (f *fakeToolRunner) Screenshot(ctx context.Context, target domain.Target) (domain.Screenshot, error) {
	f.calls = append(f.calls, "console_screenshot")
	if f.screenshotFunc != nil {
		return f.screenshotFunc(ctx, target)
	}
	return domain.Screenshot{PNG: []byte{0x89, 'P', 'N', 'G'}}, nil
}

func (f *fakeToolRunner) ConsoleLog(ctx context.Context, target domain.Target, tailLines int, previous bool) (string, error) {
	f.calls = append(f.calls, "console_log")
	if f.consoleLogFunc != nil {
		return f.consoleLogFunc(ctx, target, tailLines, previous)
	}
	return "kernel panic trace", nil
}

func (f *fakeToolRunner) ConsoleCapture(ctx context.Context, target domain.Target, durationSeconds int) (string, error) {
	f.calls = append(f.calls, "console_capture")
	if f.consoleCaptureFunc != nil {
		return f.consoleCaptureFunc(ctx, target, durationSeconds)
	}
	return "live output", nil
}

var _ = Describe("OpenAI.Triage", func() {
	It("answers directly when the model makes no tool calls", func() {
		triage, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(fakeResponseBody(validTriageJSON))
		})
		defer server.Close()

		tools := &fakeToolRunner{}
		result, usage, err := triage.Triage(context.Background(), sampleTriageRequest(), tools)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.SuspectedCause).To(Equal(domain.CauseKernelPanic))
		Expect(result.Confidence).To(Equal(domain.ConfidenceHigh))
		Expect(result.ToolsUsed).To(BeEmpty())
		Expect(tools.calls).To(BeEmpty())
		Expect(usage.InputTokens).To(Equal(int64(100)))
	})

	It("executes a requested tool call and folds its output into the final answer", func() {
		var requests int32
		triage, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&requests, 1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				w.Write(fakeFunctionCallResponseBody("call_1", "console_log", `{"tail_lines":null,"previous":null}`))
				return
			}
			w.Write(fakeResponseBody(validTriageJSON))
		})
		defer server.Close()

		tools := &fakeToolRunner{}
		result, usage, err := triage.Triage(context.Background(), sampleTriageRequest(), tools)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.ToolsUsed).To(Equal([]domain.TriageTool{domain.ToolConsoleLog}))
		Expect(tools.calls).To(Equal([]string{"console_log"}))
		Expect(atomic.LoadInt32(&requests)).To(Equal(int32(2)))
		Expect(usage.InputTokens).To(Equal(int64(150))) // 50 + 100 across both turns
	})

	It("forces a final, tool-free answer once maxToolCalls is exhausted", func() {
		var requests int32
		var capturedToolChoice []any
		_, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			capturedToolChoice = append(capturedToolChoice, body["tool_choice"])

			n := atomic.AddInt32(&requests, 1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				w.Write(fakeFunctionCallResponseBody("call_1", "console_log", `{"tail_lines":null,"previous":null}`))
				return
			}
			w.Write(fakeResponseBody(validTriageJSON))
		})
		defer server.Close()

		limited, err := model.NewOpenAI(model.OpenAIConfig{
			ClassifierModel:          "test-model",
			RequestsPerSecond:        1000,
			ConcurrentRequests:       10,
			TriageModel:              "test-triage-model",
			TriageRequestsPerSecond:  1000,
			TriageConcurrentRequests: 10,
			TriageMaxToolCalls:       1,
			TriageTimeout:            5 * time.Second,
		}, openai.NewClient(option.WithBaseURL(server.URL+"/"), option.WithAPIKey("test-key"), option.WithMaxRetries(0)))
		Expect(err).NotTo(HaveOccurred())

		tools := &fakeToolRunner{}
		result, _, err := limited.Triage(context.Background(), sampleTriageRequest(), tools)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.ToolsUsed).To(Equal([]domain.TriageTool{domain.ToolConsoleLog}))
		Expect(atomic.LoadInt32(&requests)).To(Equal(int32(2)))
		Expect(capturedToolChoice[1]).To(Equal("none"))
	})

	It("aborts the whole investigation when a tool call reports a stale target", func() {
		var requests int32
		triage, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&requests, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write(fakeFunctionCallResponseBody("call_1", "console_log", `{"tail_lines":null,"previous":null}`))
		})
		defer server.Close()

		tools := &fakeToolRunner{
			consoleLogFunc: func(ctx context.Context, target domain.Target, tailLines int, previous bool) (string, error) {
				return "", &domain.TargetError{Code: domain.ErrStaleTarget, Msg: "vmi uid changed"}
			},
		}
		_, _, err := triage.Triage(context.Background(), sampleTriageRequest(), tools)
		Expect(err).To(HaveOccurred())
		Expect(atomic.LoadInt32(&requests)).To(Equal(int32(1)))
	})

	It("feeds a non-identity tool error back to the model as text instead of aborting", func() {
		var requests int32
		var secondInput string
		triage, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&requests, 1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				w.Write(fakeFunctionCallResponseBody("call_1", "console_log", `{"tail_lines":null,"previous":null}`))
				return
			}
			body, _ := io.ReadAll(r.Body)
			secondInput = string(body)
			w.Write(fakeResponseBody(validTriageJSON))
		})
		defer server.Close()

		tools := &fakeToolRunner{
			consoleLogFunc: func(ctx context.Context, target domain.Target, tailLines int, previous bool) (string, error) {
				return "", &domain.TargetError{Code: domain.ErrUnavailable, Msg: "console mcp: timed out"}
			},
		}
		_, _, err := triage.Triage(context.Background(), sampleTriageRequest(), tools)
		Expect(err).NotTo(HaveOccurred())
		Expect(atomic.LoadInt32(&requests)).To(Equal(int32(2)))
		Expect(secondInput).To(ContainSubstring("console_log failed"))
	})

	It("requests reasoning.encrypted_content and feeds a reasoning item back into the next turn's input", func() {
		var requests int32
		var secondInput string
		var firstInclude []any
		triage, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&requests, 1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				var body map[string]any
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				if inc, ok := body["include"].([]any); ok {
					firstInclude = inc
				}
				w.Write(fakeFunctionCallResponseBodyWithReasoning("call_1", "console_log", `{"tail_lines":null,"previous":null}`, "reasoning_1"))
				return
			}
			body, _ := io.ReadAll(r.Body)
			secondInput = string(body)
			w.Write(fakeResponseBody(validTriageJSON))
		})
		defer server.Close()

		_, _, err := triage.Triage(context.Background(), sampleTriageRequest(), &fakeToolRunner{})
		Expect(err).NotTo(HaveOccurred())
		Expect(firstInclude).To(ContainElement("reasoning.encrypted_content"))
		Expect(secondInput).To(ContainSubstring(`"type":"reasoning"`))
		Expect(secondInput).To(ContainSubstring("reasoning_1"))
		Expect(secondInput).To(ContainSubstring("encrypted-blob"))
	})

	It("reports an incomplete response distinctly from a malformed one", func() {
		triage, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(fakeIncompleteResponseBody())
		})
		defer server.Close()

		_, _, err := triage.Triage(context.Background(), sampleTriageRequest(), &fakeToolRunner{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("incomplete"))
		Expect(err.Error()).To(ContainSubstring("max_output_tokens"))
	})

	It("applies the rate limiter to every model turn, not just the first", func() {
		var requests int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&requests, 1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				w.Write(fakeFunctionCallResponseBody("call_1", "console_log", `{"tail_lines":null,"previous":null}`))
				return
			}
			w.Write(fakeResponseBody(validTriageJSON))
		}))
		defer server.Close()

		// Rate low enough to block on the second turn if (and only if)
		// the limiter is applied per-turn, not just on the first request.
		limited, err := model.NewOpenAI(model.OpenAIConfig{
			ClassifierModel:          "test-model",
			RequestsPerSecond:        1000,
			ConcurrentRequests:       10,
			TriageModel:              "test-triage-model",
			TriageRequestsPerSecond:  0.001,
			TriageConcurrentRequests: 10,
			TriageMaxToolCalls:       5,
			TriageTimeout:            5 * time.Second,
		}, openai.NewClient(option.WithBaseURL(server.URL+"/"), option.WithAPIKey("test-key"), option.WithMaxRetries(0)))
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, _, err = limited.Triage(ctx, sampleTriageRequest(), &fakeToolRunner{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("rate limiter"))
		Expect(atomic.LoadInt32(&requests)).To(Equal(int32(1)))
	})

	It("rejects a malformed final response", func() {
		triage, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(fakeResponseBody(`not json`))
		})
		defer server.Close()

		_, _, err := triage.Triage(context.Background(), sampleTriageRequest(), &fakeToolRunner{})
		Expect(err).To(HaveOccurred())
	})

	It("rejects a final response with an out-of-enum suspectedCause", func() {
		triage, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(fakeResponseBody(`{"suspectedCause":"ALIENS","confidence":"HIGH","summary":"x","keyEvidence":[],"nextSteps":[]}`))
		})
		defer server.Close()

		_, _, err := triage.Triage(context.Background(), sampleTriageRequest(), &fakeToolRunner{})
		Expect(err).To(HaveOccurred())
	})

	It("truncates an oversized summary rather than failing", func() {
		huge := strings.Repeat("x", 5000)
		triage, server := newTestTriageModel(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			body, _ := json.Marshal(map[string]any{
				"suspectedCause": "KERNEL_PANIC",
				"confidence":     "HIGH",
				"summary":        huge,
				"keyEvidence":    []string{},
				"nextSteps":      []string{},
			})
			w.Write(fakeResponseBody(string(body)))
		})
		defer server.Close()

		result, _, err := triage.Triage(context.Background(), sampleTriageRequest(), &fakeToolRunner{})
		Expect(err).NotTo(HaveOccurred())
		Expect(len(result.Summary)).To(BeNumerically("<=", 1500))
	})

	It("returns an error when triage is not configured", func() {
		notConfigured, err := model.NewOpenAI(model.OpenAIConfig{
			ClassifierModel:    "test-model",
			RequestsPerSecond:  1000,
			ConcurrentRequests: 10,
		}, openai.NewClient())
		Expect(err).NotTo(HaveOccurred())

		_, _, err = notConfigured.Triage(context.Background(), sampleTriageRequest(), &fakeToolRunner{})
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("rejects invalid triage capacity settings",
		func(cfg model.OpenAIConfig) {
			cfg.ClassifierModel = "test-model"
			cfg.RequestsPerSecond = 1000
			cfg.ConcurrentRequests = 10
			_, err := model.NewOpenAI(cfg, openai.NewClient())
			Expect(err).To(HaveOccurred())
		},
		Entry("zero triage RPS", model.OpenAIConfig{TriageModel: "m", TriageConcurrentRequests: 1, TriageMaxToolCalls: 1, TriageTimeout: time.Second}),
		Entry("zero triage concurrency", model.OpenAIConfig{TriageModel: "m", TriageRequestsPerSecond: 1, TriageMaxToolCalls: 1, TriageTimeout: time.Second}),
		Entry("zero max tool calls", model.OpenAIConfig{TriageModel: "m", TriageRequestsPerSecond: 1, TriageConcurrentRequests: 1, TriageTimeout: time.Second}),
		Entry("zero timeout", model.OpenAIConfig{TriageModel: "m", TriageRequestsPerSecond: 1, TriageConcurrentRequests: 1, TriageMaxToolCalls: 1}),
	)
})
