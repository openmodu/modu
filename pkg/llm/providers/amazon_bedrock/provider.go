package amazon_bedrock

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/llm"
	"github.com/crosszan/modu/pkg/llm/utils"
)

type AmazonBedrockProvider struct{}

func (p *AmazonBedrockProvider) Api() llm.Api {
	return "bedrock-converse-stream"
}

func (p *AmazonBedrockProvider) Stream(model *llm.Model, ctx *llm.Context, opts *llm.StreamOptions) (llm.AssistantMessageEventStream, error) {
	var simple *llm.SimpleStreamOptions
	if opts != nil {
		s := llm.SimpleStreamOptions{StreamOptions: *opts}
		simple = &s
	}
	return p.StreamSimple(model, ctx, simple)
}

func (p *AmazonBedrockProvider) StreamSimple(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
	stream := utils.NewEventStream()

	go func() {
		defer stream.Close()

		output := &llm.AssistantMessage{
			Role:      "assistant",
			Api:       model.Api,
			Provider:  model.Provider,
			Model:     model.ID,
			Timestamp: time.Now().UnixMilli(),
		}

		stream.Push(llm.AssistantMessageEvent{
			Type:    "start",
			Partial: output,
		})

		accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
		secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
		sessionToken := os.Getenv("AWS_SESSION_TOKEN")
		region := os.Getenv("AWS_REGION")
		if region == "" {
			region = os.Getenv("AWS_DEFAULT_REGION")
		}
		if accessKey == "" || secretKey == "" || region == "" {
			output.StopReason = "error"
			output.ErrorMessage = "AWS credentials or region are required"
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
			})
			return
		}

		baseURL := model.BaseURL
		if baseURL == "" {
			baseURL = "https://bedrock-runtime." + region + ".amazonaws.com"
		}

		bodyPayload := map[string]any{
			"messages": buildBedrockMessages(ctx),
		}
		if ctx != nil && ctx.SystemPrompt != "" {
			bodyPayload["system"] = []map[string]any{{"text": ctx.SystemPrompt}}
		}
		if opts != nil {
			config := map[string]any{}
			if opts.MaxTokens != nil {
				config["maxTokens"] = *opts.MaxTokens
			}
			if opts.Temperature != nil {
				config["temperature"] = *opts.Temperature
			}
			if len(config) > 0 {
				bodyPayload["inferenceConfig"] = config
			}
		}

		body, err := json.Marshal(bodyPayload)
		if err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
				Error:        err,
			})
			return
		}

		url := strings.TrimRight(baseURL, "/") + "/model/" + model.ID + "/converse"
		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
				Error:        err,
			})
			return
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "application/json")
		for k, v := range model.Headers {
			req.Header.Set(k, v)
		}
		if opts != nil {
			for k, v := range opts.Headers {
				req.Header.Set(k, v)
			}
		}

		if err := signAWSRequest(req, body, accessKey, secretKey, sessionToken, region, "bedrock"); err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
				Error:        err,
			})
			return
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
				Error:        err,
			})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			data, _ := io.ReadAll(resp.Body)
			output.StopReason = "error"
			output.ErrorMessage = strings.TrimSpace(string(data))
			if output.ErrorMessage == "" {
				output.ErrorMessage = resp.Status
			}
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
			})
			return
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
				Error:        err,
			})
			return
		}

		text := extractBedrockText(data)
		if text != "" {
			output.Content = append(output.Content, &llm.TextContent{Type: "text", Text: ""})
			stream.Push(llm.AssistantMessageEvent{
				Type:         "text_start",
				ContentIndex: 0,
				Partial:      output,
			})
			if tc, ok := output.Content[0].(*llm.TextContent); ok {
				tc.Text = text
			}
			stream.Push(llm.AssistantMessageEvent{
				Type:         "text_delta",
				ContentIndex: 0,
				Delta:        text,
				Partial:      output,
			})
			stream.Push(llm.AssistantMessageEvent{
				Type:         "text_end",
				ContentIndex: 0,
				Content:      text,
				Partial:      output,
			})
		}
		output.StopReason = "stop"
		stream.Push(llm.AssistantMessageEvent{
			Type:    "done",
			Reason:  "stop",
			Partial: output,
			Message: output,
		})
	}()

	return stream, nil
}

func init() {
	llm.RegisterApiProvider(&AmazonBedrockProvider{})
}

func buildBedrockMessages(ctx *llm.Context) []map[string]any {
	if ctx == nil {
		return nil
	}
	var out []map[string]any
	for _, msg := range ctx.Messages {
		switch m := msg.(type) {
		case llm.UserMessage:
			appendBedrockMessage(&out, "user", extractBedrockInput(m.Content))
		case *llm.UserMessage:
			appendBedrockMessage(&out, "user", extractBedrockInput(m.Content))
		case llm.AssistantMessage:
			appendBedrockMessage(&out, "assistant", extractBedrockInput(m.Content))
		case *llm.AssistantMessage:
			appendBedrockMessage(&out, "assistant", extractBedrockInput(m.Content))
		case llm.ToolResultMessage:
			appendBedrockMessage(&out, "user", extractBedrockInput(m.Content))
		case *llm.ToolResultMessage:
			appendBedrockMessage(&out, "user", extractBedrockInput(m.Content))
		default:
		}
	}
	return out
}

func appendBedrockMessage(out *[]map[string]any, role string, text string) {
	if text == "" {
		return
	}
	*out = append(*out, map[string]any{
		"role": role,
		"content": []map[string]any{
			{"text": text},
		},
	})
}

func extractBedrockInput(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []llm.ContentBlock:
		var b strings.Builder
		for _, block := range v {
			switch t := block.(type) {
			case llm.TextContent:
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(t.Text)
			case *llm.TextContent:
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(t.Text)
			}
		}
		return b.String()
	case []any:
		var b strings.Builder
		for _, block := range v {
			if m, ok := block.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					if b.Len() > 0 {
						b.WriteString("\n")
					}
					b.WriteString(s)
				}
			}
		}
		return b.String()
	default:
		return ""
	}
}

func extractBedrockText(data []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	output, ok := payload["output"].(map[string]any)
	if !ok {
		return ""
	}
	msg, ok := output["message"].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := msg["content"].([]any)
	if !ok || len(content) == 0 {
		return ""
	}
	part, ok := content[0].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := part["text"].(string)
	return text
}

func signAWSRequest(req *http.Request, payload []byte, accessKey string, secretKey string, sessionToken string, region string, service string) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(payload)

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if sessionToken != "" {
		req.Header.Set("x-amz-security-token", sessionToken)
	}

	canonicalHeaders, signedHeaders := buildCanonicalHeaders(req)
	canonicalURI := req.URL.EscapedPath()
	canonicalQuery := req.URL.RawQuery
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(secretKey, dateStamp, region, service)
	signature := hmacSHA256Hex(signingKey, stringToSign)

	authHeader := "AWS4-HMAC-SHA256 Credential=" + accessKey + "/" + credentialScope + ", SignedHeaders=" + signedHeaders + ", Signature=" + signature
	req.Header.Set("Authorization", authHeader)
	return nil
}

func buildCanonicalHeaders(req *http.Request) (string, string) {
	headers := map[string]string{
		"host":                 req.URL.Host,
		"content-type":         req.Header.Get("content-type"),
		"x-amz-date":           req.Header.Get("x-amz-date"),
		"x-amz-content-sha256": req.Header.Get("x-amz-content-sha256"),
	}
	if token := req.Header.Get("x-amz-security-token"); token != "" {
		headers["x-amz-security-token"] = token
	}
	var names []string
	for name := range headers {
		names = append(names, name)
	}
	sortStrings(names)
	var canonical strings.Builder
	for _, name := range names {
		canonical.WriteString(name)
		canonical.WriteString(":")
		canonical.WriteString(strings.TrimSpace(headers[name]))
		canonical.WriteString("\n")
	}
	return canonical.String(), strings.Join(names, ";")
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func deriveSigningKey(secret string, date string, region string, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	return kSigning
}

func hmacSHA256(key []byte, data string) []byte {
	hash := hmac.New(sha256.New, key)
	hash.Write([]byte(data))
	return hash.Sum(nil)
}

func hmacSHA256Hex(key []byte, data string) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}

func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
