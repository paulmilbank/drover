package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/cloud-shuttle/drover/internal/llmproxy"
)

// RequestLogger logs requests and responses
type RequestLogger struct {
	logRequests  bool
	logResponses bool
	redactPII    bool
	file         *os.File
	mu           sync.Mutex
}

// NewRequestLogger creates a new request logger
func NewRequestLogger(logRequests, logResponses, redactPII bool) *RequestLogger {
	rl := &RequestLogger{
		logRequests:  logRequests,
		logResponses: logResponses,
		redactPII:    redactPII,
	}

	// Optionally log to file
	if logPath := os.Getenv("LLMPROXY_LOG_FILE"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			rl.file = f
			log.Printf("Logging to file: %s", logPath)
		}
	}

	return rl
}

// LogRequest logs an incoming request
func (l *RequestLogger) LogRequest(requestID string, req *llmproxy.ChatRequest, r *http.Request) {
	if !l.logRequests {
		return
	}

	entry := llmproxy.LogEntry{
		ID:        requestID,
		Timestamp: time.Now(),
		Provider:  req.Provider,
		Model:     req.Model,
		RequestID: requestID,
		Request:   *req,
		IPAddress: r.RemoteAddr,
		UserAgent: r.UserAgent(),
	}

	if l.redactPII {
		entry.Request = l.redactRequest(req)
	}

	l.log(entry)
}

// LogResponse logs a response
func (l *RequestLogger) LogResponse(requestID string, req *llmproxy.ChatRequest, resp *llmproxy.ChatResponse, duration time.Duration) {
	if !l.logResponses {
		return
	}

	entry := llmproxy.LogEntry{
		ID:         requestID,
		Timestamp:  time.Now(),
		Provider:   resp.Provider,
		Model:      resp.Model,
		RequestID:  requestID,
		Request:    *req,
		Response:   *resp,
		Duration:   duration,
		TokenUsage: resp.Usage,
	}

	if l.redactPII {
		entry.Request = l.redactRequest(req)
		entry.Response = l.redactResponse(resp)
	}

	l.log(entry)
}

// LogError logs an error
func (l *RequestLogger) LogError(requestID string, req *llmproxy.ChatRequest, err error) {
	entry := llmproxy.LogEntry{
		ID:        requestID,
		Timestamp: time.Now(),
		Provider:  req.Provider,
		Model:     req.Model,
		RequestID: requestID,
		Request:   *req,
		Error:     err.Error(),
	}

	if l.redactPII {
		entry.Request = l.redactRequest(req)
	}

	l.log(entry)
}

// LogStreamingComplete logs a completed streaming request
func (l *RequestLogger) LogStreamingComplete(requestID string, req *llmproxy.ChatRequest, totalTokens int, duration time.Duration) {
	if !l.logResponses {
		return
	}

	entry := llmproxy.LogEntry{
		ID:        requestID,
		Timestamp: time.Now(),
		Provider:  req.Provider,
		Model:     req.Model,
		RequestID: requestID,
		Request:   *req,
		Duration:  duration,
		TokenUsage: llmproxy.Usage{
			CompletionTokens: totalTokens,
			TotalTokens:      totalTokens + (len(req.Messages) * 100), // Rough estimate
		},
	}

	if l.redactPII {
		entry.Request = l.redactRequest(req)
	}

	l.log(entry)
}

// log writes the log entry
func (l *RequestLogger) log(entry llmproxy.LogEntry) {
	// Console logging
	logData, _ := json.Marshal(entry)
	log.Printf("[LLM] %s", string(logData))

	// File logging
	if l.file != nil {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.file.Write(append(logData, '\n'))
	}
}

// redactRequest redacts potentially sensitive information from a request
func (l *RequestLogger) redactRequest(req *llmproxy.ChatRequest) llmproxy.ChatRequest {
	redacted := *req

	// Redact message content (keep first 50 chars for debugging)
	for i := range redacted.Messages {
		if len(redacted.Messages[i].Content) > 50 {
			redacted.Messages[i].Content = redacted.Messages[i].Content[:50] + "... [REDACTED]"
		}
	}

	return redacted
}

// redactResponse redacts potentially sensitive information from a response
func (l *RequestLogger) redactResponse(resp *llmproxy.ChatResponse) llmproxy.ChatResponse {
	redacted := *resp

	// Redact response content (keep first 100 chars for debugging)
	for i := range redacted.Choices {
		if len(redacted.Choices[i].Message.Content) > 100 {
			redacted.Choices[i].Message.Content = redacted.Choices[i].Message.Content[:100] + "... [REDACTED]"
		}
	}

	return redacted
}

// Close closes the log file if open
func (l *RequestLogger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}
