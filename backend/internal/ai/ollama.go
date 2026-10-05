// Package ai reads occurrence photos with an open-weight vision model served
// by Ollama, so photos never leave the machine running the backend.
package ai

import (
	"bairroacao/internal/walk"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrUnavailable = errors.New("A IA está indisponível no momento. Verifique se o Ollama está rodando com o modelo configurado.")
var ErrInvalidResponse = errors.New("A IA respondeu em um formato inesperado. Tente analisar de novo ou preencha manualmente.")

// DefaultQuestion is used when the model is unsure but forgot to ask.
const DefaultQuestion = "Não consegui identificar o problema pela foto. Pode descrever o que está errado nesse ponto?"

type Request struct {
	Image        []byte
	Neighborhood string
	Note         string
}

type Ollama struct {
	baseURL, model string
	http           *http.Client
}

func NewOllama(rawURL, model string, timeout time.Duration) (*Ollama, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(model) == "" || timeout <= 0 {
		return nil, errors.New("invalid Ollama configuration")
	}
	return &Ollama{strings.TrimRight(rawURL, "/"), model, &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (o *Ollama) Model() string { return o.model }

var categoryIDs = func() []string {
	ids := make([]string, len(walk.Categories))
	for i, c := range walk.Categories {
		ids[i] = string(c.ID)
	}
	return ids
}()

var responseSchema = map[string]any{
	"type": "object", "additionalProperties": false,
	"required": []string{"category", "title", "description", "confidence", "question"},
	"properties": map[string]any{
		"category":    map[string]any{"type": "string", "enum": categoryIDs},
		"title":       map[string]any{"type": "string"},
		"description": map[string]any{"type": "string"},
		"confidence":  map[string]any{"type": "string", "enum": []string{"alta", "media", "baixa"}},
		"question":    map[string]any{"type": "string"},
	},
}

// Analyze returns the suggestion and whether the person needs to add details.
func (o *Ollama) Analyze(ctx context.Context, req Request) (walk.Suggestion, walk.AIStatus, error) {
	envelope, err := json.Marshal(struct {
		Neighborhood string `json:"BAIRRO"`
		Note         string `json:"RELATO"`
	}{req.Neighborhood, req.Note})
	if err != nil {
		return walk.Suggestion{}, "", ErrInvalidResponse
	}
	payload := map[string]any{
		"model": o.model, "stream": false, "keep_alive": "15m",
		"options": map[string]any{"temperature": 0.1, "num_predict": 400, "num_ctx": 4096},
		"format":  responseSchema,
		"messages": []map[string]any{
			{"role": "system", "content": SystemPrompt},
			{"role": "user", "content": string(envelope), "images": []string{base64.StdEncoding.EncodeToString(req.Image)}},
		},
	}
	content, err := o.chat(ctx, payload)
	if err != nil {
		return walk.Suggestion{}, "", err
	}
	return parseSuggestion(content, o.model)
}

func parseSuggestion(content, model string) (walk.Suggestion, walk.AIStatus, error) {
	var raw struct {
		Category    string `json:"category"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Confidence  string `json:"confidence"`
		Question    string `json:"question"`
	}
	if json.Unmarshal([]byte(content), &raw) != nil {
		return walk.Suggestion{}, "", ErrInvalidResponse
	}
	s := walk.Suggestion{
		Category:    walk.Category(raw.Category),
		Title:       clip(strings.TrimRight(oneLine(raw.Title), "."), 80),
		Description: clip(strings.TrimSpace(raw.Description), 1000),
		Confidence:  raw.Confidence,
		Question:    clip(oneLine(raw.Question), 300),
		Model:       model,
	}
	if _, ok := s.Category.Info(); !ok || (s.Confidence != "alta" && s.Confidence != "media" && s.Confidence != "baixa") {
		return walk.Suggestion{}, "", ErrInvalidResponse
	}
	if s.Confidence == "baixa" || s.Title == "" {
		if s.Question == "" {
			s.Question = DefaultQuestion
		}
		return s, walk.AINeedsInfo, nil
	}
	s.Question = ""
	return s, walk.AIDone, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}

func (o *Ollama) chat(ctx context.Context, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", ErrInvalidResponse
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.http.Do(req)
	if err != nil {
		return "", ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(data) > 65536 {
		return "", ErrInvalidResponse
	}
	var response struct {
		Done    bool `json:"done"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &response) != nil || !response.Done {
		return "", ErrInvalidResponse
	}
	return response.Message.Content, nil
}

// Healthy reports whether the daemon answers and the configured model is installed.
func (o *Ollama) Healthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	res, err := o.http.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&payload) != nil {
		return false
	}
	for _, m := range payload.Models {
		if m.Name == o.model || m.Name == o.model+":latest" {
			return true
		}
	}
	return false
}
