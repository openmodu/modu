package genimagerepo

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/openmodu/modu/consts/provider"
	genimagevo "github.com/openmodu/modu/vo/gen_image_vo"
)

type geminiImageImpl struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewGeminiImageImpl(baseURL, apiKey string) ImageGenRepo {
	return &geminiImageImpl{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      string(provider.ImageModel_Gemini),
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

func (i *geminiImageImpl) Name() string {
	return string(provider.ImageProvider_Gemini)
}

func (i *geminiImageImpl) Generate(ctx context.Context, req *genimagevo.GenImageRequest) (*genimagevo.GenImageResponse, error) {
	if req.UserPrompt == "" {
		return nil, fmt.Errorf("user prompt is required")
	}

	prompt := req.UserPrompt
	if req.SystemPrompt != "" {
		prompt = req.SystemPrompt + "\n\n" + req.UserPrompt
	}

	apiReq := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]string{{"text": prompt}}},
		},
	}
	body, _ := json.Marshal(apiReq)

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", i.baseURL, i.model, i.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := i.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed (%d): %s", httpResp.StatusCode, string(data))
	}

	var apiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text,omitempty"`
					InlineData *struct {
						MimeType string `json:"mimeType,omitempty"`
						Data     string `json:"data"`
					} `json:"inlineData,omitempty"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		ModelVersion  string `json:"modelVersion"`
		UsageMetadata struct {
			PromptTokenCount int `json:"promptTokenCount"`
			TotalTokenCount  int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(data, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}

	resp := &genimagevo.GenImageResponse{
		Model:        apiResp.ModelVersion,
		ProviderName: i.Name(),
		Usage: &genimagevo.UsageInfo{
			PromptTokens: apiResp.UsageMetadata.PromptTokenCount,
			TotalTokens:  apiResp.UsageMetadata.TotalTokenCount,
		},
		RawResponse: apiResp,
	}

	for _, c := range apiResp.Candidates {
		for _, part := range c.Content.Parts {
			if part.InlineData != nil && part.InlineData.Data != "" {
				imgData, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
				if err != nil {
					continue
				}
				resp.Images = append(resp.Images, &genimagevo.Image{
					Data:     imgData,
					MimeType: part.InlineData.MimeType,
				})
			}
		}
	}

	return resp, nil
}
