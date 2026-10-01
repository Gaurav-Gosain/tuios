// Command fakeopenai is a model provider for the real-Crush test. It speaks
// the OpenAI chat completions API that Crush's openai-compat provider calls,
// streams canned answers, and costs nothing: no request leaves the machine.
//
// A request whose last message is a tool result is answered with text, which
// ends the turn. A request that offers tools and whose last user message
// holds RUN-TOOL is answered with one call to Crush's bash tool, which makes
// Crush ask for permission. Everything else, a title request included, is
// answered with a short text.
//
// It listens on 127.0.0.1 at a free port and prints the base URL on its
// first line.
//
// FAKEOPENAI_COMMAND and FAKEOPENAI_DESC set the bash call's command and
// description, and FAKEOPENAI_REPLY the text of a plain answer, so a test
// can make Crush show a given command or quote given text in its chat.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type request struct {
	Messages []message        `json:"messages"`
	Tools    []map[string]any `json:"tools"`
	Stream   bool             `json:"stream"`
}

func text(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, p := range v {
			if m, ok := p.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	}
	return ""
}

func main() {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("http://%s/v1\n", l.Addr())
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"fake-model","object":"model"}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req request
		_ = json.NewDecoder(r.Body).Decode(&req)
		last := message{}
		lastUser := ""
		if n := len(req.Messages); n > 0 {
			last = req.Messages[n-1]
		}
		for _, m := range req.Messages {
			if m.Role == "user" {
				lastUser = text(m.Content)
			}
		}
		toolCall := last.Role == "user" && len(req.Tools) > 0 && strings.Contains(lastUser, "RUN-TOOL")
		reply := "Done. The command ran."
		if last.Role != "tool" && !toolCall {
			reply = envOr("FAKEOPENAI_REPLY", "Fake answer")
		}
		fmt.Fprintf(os.Stderr, "request: last=%s tools=%d toolcall=%v\n", last.Role, len(req.Tools), toolCall)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		send := func(v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if fl != nil {
				fl.Flush()
			}
		}
		chunk := func(delta map[string]any, finish any) map[string]any {
			return map[string]any{
				"id": "chatcmpl-fake", "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "fake-model",
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			}
		}
		// A turn that takes a moment, so working is seen.
		time.Sleep(1500 * time.Millisecond)
		if toolCall {
			args, _ := json.Marshal(map[string]string{
				"command":     envOr("FAKEOPENAI_COMMAND", "touch tuios-e2e-marker"),
				"description": envOr("FAKEOPENAI_DESC", "Create a marker file"),
			})
			send(chunk(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"index": 0, "id": "call_e2e_1", "type": "function",
				"function": map[string]any{"name": "bash", "arguments": string(args)},
			}}}, nil))
			send(chunk(map[string]any{}, "tool_calls"))
		} else {
			send(chunk(map[string]any{"role": "assistant", "content": reply}, nil))
			send(chunk(map[string]any{}, "stop"))
		}
		send(map[string]any{"id": "chatcmpl-fake", "object": "chat.completion.chunk", "choices": []any{},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	})
	_ = http.Serve(l, mux)
}

// envOr is the variable's value, or def when it is unset or empty.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
