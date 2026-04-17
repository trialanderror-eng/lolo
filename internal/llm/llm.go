// Package llm defines the minimal contract a narrative generator must
// satisfy. Implementations are expected to call a LOCAL inference endpoint
// the operator has stood up (Ollama, llama.cpp server, vLLM). No hosted
// API calls — incident data must not leave the operator's environment.
package llm

import "context"

type Client interface {
	// Narrate returns a short natural-language response to the user prompt,
	// bounded by the implementation's internal max tokens. Implementations
	// should honor ctx cancellation so callers can enforce timeouts.
	Narrate(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
