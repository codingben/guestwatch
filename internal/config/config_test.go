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
		Expect(cfg.Scan.WorkerCount).To(Equal(config.DefaultWorkerCount))
		Expect(cfg.Scan.KubeAPIQPS).To(Equal(config.DefaultKubeAPIQPS))
		Expect(cfg.Scan.KubeAPIBurst).To(Equal(config.DefaultKubeAPIBurst))
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

	It("defaults dashboard.addr when it is not set", func() {
		cfg, err := config.Load(strings.NewReader(validYAML))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Dashboard.Addr).To(Equal(config.DefaultDashboardAddr))
	})

	It("accepts an explicit dashboard.addr", func() {
		yaml := validYAML + "\ndashboard:\n  addr: \"127.0.0.1:9090\"\n"
		cfg, err := config.Load(strings.NewReader(yaml))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Dashboard.Addr).To(Equal("127.0.0.1:9090"))
	})

	It("rejects a malformed dashboard.addr", func() {
		yaml := validYAML + "\ndashboard:\n  addr: \"not-a-host-port\"\n"
		_, err := config.Load(strings.NewReader(yaml))
		Expect(err).To(HaveOccurred())
	})

	It("accepts dashboard.maxObservations left at zero (the store's own default applies)", func() {
		cfg, err := config.Load(strings.NewReader(validYAML))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Dashboard.MaxObservations).To(Equal(0))
	})

	It("accepts an explicit dashboard.maxObservations", func() {
		yaml := validYAML + "\ndashboard:\n  maxObservations: 5000\n"
		cfg, err := config.Load(strings.NewReader(yaml))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Dashboard.MaxObservations).To(Equal(5000))
	})

	It("rejects a negative dashboard.maxObservations", func() {
		yaml := validYAML + "\ndashboard:\n  maxObservations: -1\n"
		_, err := config.Load(strings.NewReader(yaml))
		Expect(err).To(HaveOccurred())
	})

	It("defaults dashboard.staticDir to where the Dockerfile places the built UI", func() {
		cfg, err := config.Load(strings.NewReader(validYAML))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Dashboard.StaticDir).To(Equal(config.DefaultDashboardStaticDir))
	})

	It("accepts an explicit dashboard.staticDir", func() {
		yaml := validYAML + "\ndashboard:\n  staticDir: \"/custom/ui\"\n"
		cfg, err := config.Load(strings.NewReader(yaml))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Dashboard.StaticDir).To(Equal("/custom/ui"))
	})

	It("defaults mcp.wakeScreen to true when unset", func() {
		cfg, err := config.Load(strings.NewReader(validYAML))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.MCP.WakeScreen).NotTo(BeNil())
		Expect(*cfg.MCP.WakeScreen).To(BeTrue())
	})

	It("accepts an explicit mcp.wakeScreen: false", func() {
		yaml := strings.Replace(validYAML,
			`consoleURL: "https://kubevirt-console-mcp:8443/mcp"`,
			"consoleURL: \"https://kubevirt-console-mcp:8443/mcp\"\n  wakeScreen: false",
			1)
		cfg, err := config.Load(strings.NewReader(yaml))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.MCP.WakeScreen).NotTo(BeNil())
		Expect(*cfg.MCP.WakeScreen).To(BeFalse())
	})

	It("leaves triage disabled by default and skips its validation entirely", func() {
		cfg, err := config.Load(strings.NewReader(validYAML))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Triage.Enabled).To(BeFalse())
		// Defaults still apply even though triage is off.
		Expect(cfg.Triage.MaxToolCalls).To(Equal(config.DefaultTriageMaxToolCalls))
		Expect(cfg.Triage.Deadline).To(Equal(config.DefaultTriageDeadline))
		Expect(cfg.Triage.RPS).To(Equal(config.DefaultTriageRPS))
		Expect(cfg.Triage.Concurrency).To(Equal(config.DefaultTriageConcurrency))
	})

	// Extends validYAML's existing privacy mapping rather than adding a
	// second "privacy:" key, which yaml.v3 rejects as a duplicate.
	validTriageYAML := strings.Replace(validYAML,
		"consoleEvidenceEgressAcknowledged: true",
		"consoleEvidenceEgressAcknowledged: true\n  triageEvidenceEgressAcknowledged: true",
		1,
	) + "triage:\n  enabled: true\n  model: \"gpt-reasoning-1\"\n"

	It("accepts a fully valid enabled triage block and applies its capacity defaults", func() {
		cfg, err := config.Load(strings.NewReader(validTriageYAML))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Triage.Enabled).To(BeTrue())
		Expect(cfg.Triage.Model).To(Equal("gpt-reasoning-1"))
		Expect(cfg.Triage.MaxToolCalls).To(Equal(config.DefaultTriageMaxToolCalls))
		Expect(cfg.Triage.Deadline).To(Equal(config.DefaultTriageDeadline))
		Expect(cfg.Triage.RPS).To(Equal(config.DefaultTriageRPS))
		Expect(cfg.Triage.Concurrency).To(Equal(config.DefaultTriageConcurrency))
	})

	It("rejects enabled triage without an egress acknowledgement distinct from the console one", func() {
		yaml := validYAML + "\ntriage:\n  enabled: true\n  model: \"gpt-reasoning-1\"\n"
		_, err := config.Load(strings.NewReader(yaml))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("triageEvidenceEgressAcknowledged"))
	})

	DescribeTable("rejects an invalid enabled triage block",
		func(yaml string) {
			_, err := config.Load(strings.NewReader(yaml))
			Expect(err).To(HaveOccurred())
		},
		Entry("empty model", strings.Replace(validTriageYAML, `model: "gpt-reasoning-1"`, `model: ""`, 1)),
		Entry("REQUIRED_ placeholder model", strings.Replace(validTriageYAML, `model: "gpt-reasoning-1"`, `model: "REQUIRED_MODEL_ID"`, 1)),
		// 0 is indistinguishable from "unset" once applyDefaults runs, so
		// these use negative/out-of-range values instead.
		Entry("negative maxToolCalls", validTriageYAML+"  maxToolCalls: -1\n"),
		Entry("maxToolCalls too high", validTriageYAML+"  maxToolCalls: 31\n"),
		Entry("negative deadline", validTriageYAML+"  deadline: -1s\n"),
		Entry("negative rps", validTriageYAML+"  rps: -1\n"),
		Entry("negative concurrency", validTriageYAML+"  concurrency: -1\n"),
	)

	It("does not require a triage block to be present at all", func() {
		Expect(strings.Contains(validYAML, "triage:")).To(BeFalse())
		_, err := config.Load(strings.NewReader(validYAML))
		Expect(err).NotTo(HaveOccurred())
	})
})
