# Deploy

Deploy to an existing Kubernetes cluster.

### 1. Configure

Set the target namespace, model, and OpenAI API key:

```bash
export NAMESPACE=my-namespace
export MODEL_ID=gpt-5.6-luna
export OPENAI_API_KEY=your-key
```

### 2. Deploy

Create the model secret and deploy the AI agent with Console MCP:

```bash
envsubst < model-secret.example.yaml | kubectl apply -f -
envsubst < kubevirt-ai-agent.yaml | kubectl apply -f -
```

### 3. Access the Dashboard

Forward the service to your local machine:

```bash
kubectl -n "$NAMESPACE" port-forward svc/kubevirt-ai-agent 8080:80
```

Open http://localhost:8080 in your browser.
