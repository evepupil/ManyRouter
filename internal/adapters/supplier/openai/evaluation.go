package openai

import (
	"context"
	"net/http"

	evaluationapp "github.com/evepupil/ManyRouter/internal/application/evaluation"
)

type EvaluationProber struct {
	client *ProbeClient
}

func NewEvaluationProber(client *http.Client) (*EvaluationProber, error) {
	probeClient, err := NewProbeClient(client)
	if err != nil {
		return nil, err
	}
	return &EvaluationProber{client: probeClient}, nil
}

func (prober *EvaluationProber) Probe(
	ctx context.Context,
	baseURL string,
	key []byte,
	request evaluationapp.ProbeRequest,
) (evaluationapp.ProbeResult, error) {
	result, err := prober.client.Probe(ctx, baseURL, key, ProbeRequest{
		Model: request.Model, Prompt: request.Prompt, Temperature: request.Temperature,
		TopP: request.TopP, MaxTokens: request.MaxTokens, Stream: request.Stream,
	})
	return evaluationapp.ProbeResult{
		Text: result.Text, ResponseModel: result.ResponseModel, HTTPStatus: result.HTTPStatus,
		FinishReason: result.FinishReason, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens,
		FirstTokenMillis: result.FirstTokenMillis, TotalMillis: result.TotalMillis,
		StreamCompleted: result.StreamCompleted,
	}, err
}

var _ evaluationapp.Prober = (*EvaluationProber)(nil)
