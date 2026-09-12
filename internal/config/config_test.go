package config_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/codingben/kubevirt-ai-agent/internal/config"
)

const validYAML = `
scan:
  namespaces: [payments, checkout]
  labelSelector: "monitoring.kubevirt.io/critical=true"
  interval: 5m
  perNodeConcurrency: 1
mcp:
  consoleURL: "https://kubevirt-console-mcp:8443/mcp"
model:
  classifier: "gpt-vision-1"
privacy:
  consoleEvidenceEgressAcknowledged: true
`

var _ = Describe("config.Load", func() {
	It("accepts a fully valid config and applies capacity defaults", func() {
		cfg, err := config.Load(strings.NewReader(validYAML))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Scan.Namespaces).To(Equal([]string{"payments", "checkout"}))
		Expect(cfg.Scan.ClassifierRPS).To(Equal(config.DefaultClassifierRPS))
		Expect(cfg.Scan.ClassifierConcurrency).To(Equal(config.DefaultClassifierConcurrency))
		Expect(cfg.Scan.FirstPassDeadline).To(Equal(config.DefaultFirstPassDeadline))
	})

	It("accepts plain http to a loopback console URL", func() {
		yaml := strings.Replace(validYAML, "https://kubevirt-console-mcp:8443/mcp", "http://127.0.0.1:8081/mcp", 1)
		_, err := config.Load(strings.NewReader(yaml))
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects unknown fields", func() {
		_, err := config.Load(strings.NewReader(validYAML + "\nextra: true\n"))
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("rejects an invalid document",
		func(yaml string) {
			_, err := config.Load(strings.NewReader(yaml))
			Expect(err).To(HaveOccurred())
		},
		Entry("empty namespace list", strings.Replace(validYAML, "namespaces: [payments, checkout]", "namespaces: []", 1)),
		Entry("duplicate namespaces", strings.Replace(validYAML, "namespaces: [payments, checkout]", "namespaces: [payments, payments]", 1)),
		Entry("empty namespace entry", strings.Replace(validYAML, "namespaces: [payments, checkout]", `namespaces: ["", checkout]`, 1)),
		Entry("invalid label selector", strings.Replace(validYAML, `labelSelector: "monitoring.kubevirt.io/critical=true"`, `labelSelector: "=="`, 1)),
		Entry("insecure http console URL", strings.Replace(validYAML, "https://kubevirt-console-mcp:8443/mcp", "http://kubevirt-console-mcp:8443/mcp", 1)),
		Entry("empty console URL", strings.Replace(validYAML, `consoleURL: "https://kubevirt-console-mcp:8443/mcp"`, `consoleURL: ""`, 1)),
		Entry("empty model classifier", strings.Replace(validYAML, `classifier: "gpt-vision-1"`, `classifier: ""`, 1)),
		Entry("REQUIRED_ sentinel model classifier", strings.Replace(validYAML, `classifier: "gpt-vision-1"`, `classifier: "REQUIRED_MODEL_ID"`, 1)),
		Entry("another REQUIRED_ sentinel form", strings.Replace(validYAML, `classifier: "gpt-vision-1"`, `classifier: "REQUIRED_SOMETHING_ELSE"`, 1)),
		Entry("missing privacy acknowledgement", strings.Replace(validYAML, "consoleEvidenceEgressAcknowledged: true", "consoleEvidenceEgressAcknowledged: false", 1)),
		Entry("zero interval", strings.Replace(validYAML, "interval: 5m", "interval: 0s", 1)),
		Entry("zero perNodeConcurrency", strings.Replace(validYAML, "perNodeConcurrency: 1", "perNodeConcurrency: 0", 1)),
	)

	It("rejects a negative classifierRPS even when set explicitly", func() {
		yaml := strings.Replace(validYAML, "interval: 5m", "interval: 5m\n  classifierRPS: -1", 1)
		_, err := config.Load(strings.NewReader(yaml))
		Expect(err).To(HaveOccurred())
	})
})
