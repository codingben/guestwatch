package model_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
