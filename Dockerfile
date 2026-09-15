FROM --platform=$BUILDPLATFORM node:22-alpine AS ui-builder

WORKDIR /ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26 AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o kubevirt-ai-agent ./cmd/kubevirt-ai-agent

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

COPY --from=builder /app/kubevirt-ai-agent /usr/local/bin/kubevirt-ai-agent
COPY --from=ui-builder /ui/dist /opt/guestwatch/ui

# Run as a non-root, numeric user so the image works under restricted
# PodSecurity / OpenShift SCCs.
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/kubevirt-ai-agent"]
