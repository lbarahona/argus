package tui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/signoz"
)

const tuiSystemPrompt = `You are an expert Site Reliability Engineer (SRE) in an interactive troubleshooting session.

You have access to live observability data from a Signoz instance, which is provided as context with each message.
The user is an SRE investigating issues in real time.

Your role:
- Analyze logs, metrics, and traces to identify issues
- Remember context from earlier in the conversation — the user may ask follow-up questions
- Provide clear, actionable insights
- Prioritize by severity and impact
- Suggest root causes and remediation steps
- When you reference data, cite specifics (timestamps, service names, error messages)

Format your responses with markdown. Be concise but thorough.`

var (
	bannerStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(0, 2)

	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7C3AED")).
			Bold(true)

	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))

	accentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#3B82F6")).
			Bold(true)

	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))
)

// Options configures the TUI session.
type Options struct {
	InstanceKey  string
	InstanceName string
	AnthropicKey string
	MaxHistory   int
}

// Session holds the state for an interactive TUI session.
type Session struct {
	client       signoz.SignozQuerier
	instanceKey  string
	instanceName string
	anthropicKey string
	history      []ai.Message
	maxHistory   int
	stdin        io.Reader
	stdout       io.Writer
}

// New creates a new TUI session.
func New(client signoz.SignozQuerier, opts Options) *Session {
	maxHistory := opts.MaxHistory
	if maxHistory <= 0 {
		maxHistory = 20
	}
	name := opts.InstanceName
	if name == "" {
		name = opts.InstanceKey
	}
	return &Session{
		client:       client,
		instanceKey:  opts.InstanceKey,
		instanceName: name,
		anthropicKey: opts.AnthropicKey,
		maxHistory:   maxHistory,
		stdin:        os.Stdin,
		stdout:       os.Stdout,
	}
}

// Run starts the interactive REPL loop.
func (s *Session) Run(ctx context.Context) error {
	s.printBanner()

	scanner := bufio.NewScanner(s.stdin)
	for {
		fmt.Fprint(s.stdout, promptStyle.Render("argus> "))

		if !scanner.Scan() {
			break // EOF or error
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		if input == "exit" || input == "quit" {
			fmt.Fprintln(s.stdout, mutedStyle.Render("Session ended."))
			return nil
		}

		if s.handleCommand(input) {
			continue
		}

		// Gather live Signoz context
		fmt.Fprintf(s.stdout, "\n  %s\n\n", mutedStyle.Render(fmt.Sprintf("Gathering data from %s...", accentStyle.Render(s.instanceKey))))

		signozContext := s.gatherContext(ctx)

		// Build the user message with Signoz data appended
		userContent := input
		if signozContext != "" {
			userContent += "\n\n---\n[Live Signoz data from " + s.instanceKey + "]\n" + signozContext
		}

		s.history = append(s.history, ai.Message{Role: "user", Content: userContent})

		// Stream AI response, capturing it for history
		var responseBuf bytes.Buffer
		multiWriter := io.MultiWriter(s.stdout, &responseBuf)

		analyzer := ai.New(s.anthropicKey)
		if err := analyzer.AnalyzeWithHistory(tuiSystemPrompt, s.history, multiWriter); err != nil {
			fmt.Fprintf(s.stdout, "\n%s %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true).Render("Error:"), err)
			// Remove the failed user message so the conversation stays clean
			s.history = s.history[:len(s.history)-1]
		} else {
			s.history = append(s.history, ai.Message{Role: "assistant", Content: responseBuf.String()})
			s.trimHistory()
		}

		fmt.Fprintln(s.stdout)
		fmt.Fprintln(s.stdout, separatorStyle.Render(strings.Repeat("\u2500", 40)))
		fmt.Fprintln(s.stdout)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading input: %w", err)
	}

	fmt.Fprintln(s.stdout, mutedStyle.Render("\nSession ended."))
	return nil
}

func (s *Session) printBanner() {
	banner := fmt.Sprintf("Argus Interactive Mode\nInstance: %s", s.instanceName)
	fmt.Fprintln(s.stdout, bannerStyle.Render(banner))
	fmt.Fprintln(s.stdout)
	fmt.Fprintln(s.stdout, mutedStyle.Render("  Commands: /help, /clear, /history, exit"))
	fmt.Fprintln(s.stdout)
}

func (s *Session) handleCommand(input string) bool {
	switch input {
	case "/clear":
		s.history = nil
		fmt.Fprintln(s.stdout, mutedStyle.Render("  Conversation history cleared."))
		fmt.Fprintln(s.stdout)
		return true
	case "/help":
		fmt.Fprintln(s.stdout)
		fmt.Fprintln(s.stdout, accentStyle.Render("  Available commands:"))
		fmt.Fprintln(s.stdout, "    /help     Show this help message")
		fmt.Fprintln(s.stdout, "    /clear    Clear conversation history")
		fmt.Fprintln(s.stdout, "    /history  Show conversation turn count")
		fmt.Fprintln(s.stdout, "    exit      End the session")
		fmt.Fprintln(s.stdout)
		return true
	case "/history":
		turns := len(s.history) / 2
		fmt.Fprintf(s.stdout, "  %s %d turns (%d messages)\n\n",
			mutedStyle.Render("History:"), turns, len(s.history))
		return true
	default:
		return false
	}
}

func (s *Session) gatherContext(ctx context.Context) string {
	var b strings.Builder

	// Services overview
	services, err := s.client.ListServices(ctx)
	if err == nil && len(services) > 0 {
		b.WriteString("## Services\n")
		for _, svc := range services {
			rate := svc.ErrorRate
			if svc.NumCalls > 0 && rate == 0 && svc.NumErrors > 0 {
				rate = float64(svc.NumErrors) / float64(svc.NumCalls) * 100
			}
			b.WriteString(fmt.Sprintf("- %s: %d calls, %d errors (%.1f%%)\n",
				svc.Name, svc.NumCalls, svc.NumErrors, rate))
		}
	}

	// Recent error logs
	result, err := s.client.QueryLogs(ctx, "", 15, 20, "ERROR")
	if err == nil && len(result.Logs) > 0 {
		b.WriteString("\n## Recent Error Logs\n")
		for _, log := range result.Logs {
			body := log.Body
			if len(body) > 200 {
				body = body[:200]
			}
			svc := log.ServiceName
			if svc == "" {
				svc = "unknown"
			}
			b.WriteString(fmt.Sprintf("- [%s] %s: %s\n",
				log.Timestamp.Format("15:04:05"), svc, body))
		}
	}

	return b.String()
}

func (s *Session) trimHistory() {
	// maxHistory is counted in messages (user + assistant pairs)
	if len(s.history) > s.maxHistory {
		// Drop from the front, keeping the most recent messages.
		// Always drop in pairs to maintain user/assistant alternation.
		excess := len(s.history) - s.maxHistory
		if excess%2 != 0 {
			excess++
		}
		if excess > 0 && excess < len(s.history) {
			s.history = s.history[excess:]
		}
	}
}

