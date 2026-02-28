package deps

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// Edge represents a dependency between two services.
type Edge struct {
	From       string
	To         string
	Calls      int
	Errors     int
	ErrorRate  float64
	AvgLatency float64 // ms
	P99Latency float64 // ms
}

// ServiceNode holds aggregated stats for a service in the graph.
type ServiceNode struct {
	Name       string
	TotalCalls int
	TotalErrors int
	Upstream   []string // services that call this one
	Downstream []string // services this one calls
	IsRoot     bool     // no upstream callers
	IsLeaf     bool     // no downstream calls
}

// DependencyMap holds the complete service dependency graph.
type DependencyMap struct {
	GeneratedAt time.Time
	Duration    int // minutes
	Instance    string
	Nodes       map[string]*ServiceNode
	Edges       []Edge
	AISummary   string
}

// Options configures dependency map generation.
type Options struct {
	Querier   signoz.SignozQuerier
	Instance  string
	Duration  int    // minutes
	Service   string // filter to show only deps for this service
	Format    string // "table" or "markdown"
	AI        bool
	AIKey     string
	Writer    io.Writer
}

// Generate builds a service dependency map from trace data.
func Generate(ctx context.Context, opts Options) (*DependencyMap, error) {
	if opts.Duration <= 0 {
		opts.Duration = 60
	}

	// Get all services
	services, err := opts.Querier.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	dm := &DependencyMap{
		GeneratedAt: time.Now(),
		Duration:    opts.Duration,
		Instance:    opts.Instance,
		Nodes:       make(map[string]*ServiceNode),
		Edges:       []Edge{},
	}

	// Gather traces from each service to find parent-child relationships
	edgeMap := make(map[string]*Edge) // "from->to" => edge

	for _, svc := range services {
		result, err := opts.Querier.QueryTraces(ctx, svc.Name, opts.Duration, 500)
		if err != nil {
			continue // skip services with query errors
		}

		// Build span index: spanID -> trace entry
		spanIndex := make(map[string]*types.TraceEntry)
		for i := range result.Traces {
			t := &result.Traces[i]
			spanIndex[t.SpanID] = t
		}

		// Find cross-service edges by matching parent spans
		for i := range result.Traces {
			span := &result.Traces[i]
			if span.ParentSpanID == "" {
				continue
			}
			parent, ok := spanIndex[span.ParentSpanID]
			if !ok {
				continue
			}
			if parent.ServiceName == span.ServiceName {
				continue // same service, skip
			}

			key := parent.ServiceName + "->" + span.ServiceName
			edge, exists := edgeMap[key]
			if !exists {
				edge = &Edge{
					From: parent.ServiceName,
					To:   span.ServiceName,
				}
				edgeMap[key] = edge
			}
			edge.Calls++
			latMs := span.DurationMs()
			edge.AvgLatency += latMs
			if latMs > edge.P99Latency {
				edge.P99Latency = latMs
			}
			if span.StatusCode == "STATUS_CODE_ERROR" || span.StatusCode == "ERROR" {
				edge.Errors++
			}
		}
	}

	// Finalize edges
	for _, edge := range edgeMap {
		if edge.Calls > 0 {
			edge.AvgLatency = edge.AvgLatency / float64(edge.Calls)
			edge.ErrorRate = float64(edge.Errors) / float64(edge.Calls) * 100
		}
		dm.Edges = append(dm.Edges, *edge)
	}

	// Sort edges by call volume
	sort.Slice(dm.Edges, func(i, j int) bool {
		return dm.Edges[i].Calls > dm.Edges[j].Calls
	})

	// Build node map from edges
	for _, edge := range dm.Edges {
		if _, ok := dm.Nodes[edge.From]; !ok {
			dm.Nodes[edge.From] = &ServiceNode{Name: edge.From}
		}
		if _, ok := dm.Nodes[edge.To]; !ok {
			dm.Nodes[edge.To] = &ServiceNode{Name: edge.To}
		}
		from := dm.Nodes[edge.From]
		to := dm.Nodes[edge.To]

		from.TotalCalls += edge.Calls
		from.Downstream = appendUnique(from.Downstream, edge.To)
		to.TotalCalls += edge.Calls
		to.TotalErrors += edge.Errors
		to.Upstream = appendUnique(to.Upstream, edge.From)
	}

	// Mark roots and leaves
	for _, node := range dm.Nodes {
		node.IsRoot = len(node.Upstream) == 0
		node.IsLeaf = len(node.Downstream) == 0
	}

	// Also add services from the listing that have no edges (isolated)
	for _, svc := range services {
		if _, ok := dm.Nodes[svc.Name]; !ok {
			dm.Nodes[svc.Name] = &ServiceNode{
				Name:   svc.Name,
				IsRoot: true,
				IsLeaf: true,
			}
		}
	}

	// Filter if service specified
	if opts.Service != "" {
		dm = filterForService(dm, opts.Service)
	}

	// AI analysis
	if opts.AI && opts.AIKey != "" {
		prompt := buildAIPrompt(dm)
		analyzer := ai.New(opts.AIKey)
		var sb strings.Builder
		if err := analyzer.Analyze(prompt, &sb); err == nil {
			dm.AISummary = sb.String()
		}
	}

	return dm, nil
}

func filterForService(dm *DependencyMap, service string) *DependencyMap {
	filtered := &DependencyMap{
		GeneratedAt: dm.GeneratedAt,
		Duration:    dm.Duration,
		Instance:    dm.Instance,
		Nodes:       make(map[string]*ServiceNode),
	}

	// Keep edges involving the target service
	related := map[string]bool{service: true}
	for _, edge := range dm.Edges {
		if edge.From == service || edge.To == service {
			filtered.Edges = append(filtered.Edges, edge)
			related[edge.From] = true
			related[edge.To] = true
		}
	}

	// Keep related nodes
	for name, node := range dm.Nodes {
		if related[name] {
			filtered.Nodes[name] = node
		}
	}

	return filtered
}

// RenderTable prints the dependency map as a terminal table.
func RenderTable(w io.Writer, dm *DependencyMap) {
	fmt.Fprintln(w, output.AccentStyle.Render(fmt.Sprintf("━━━ Service Dependency Map — last %d min ━━━", dm.Duration)))
	fmt.Fprintln(w)

	// Summary
	roots := []string{}
	leaves := []string{}
	for _, node := range dm.Nodes {
		if node.IsRoot && !node.IsLeaf {
			roots = append(roots, node.Name)
		}
		if node.IsLeaf && !node.IsRoot {
			leaves = append(leaves, node.Name)
		}
	}
	sort.Strings(roots)
	sort.Strings(leaves)

	fmt.Fprintf(w, "  Services: %d    Edges: %d\n", len(dm.Nodes), len(dm.Edges))
	if len(roots) > 0 {
		fmt.Fprintf(w, "  Entry points: %s\n", strings.Join(roots, ", "))
	}
	if len(leaves) > 0 {
		fmt.Fprintf(w, "  Leaf services: %s\n", strings.Join(leaves, ", "))
	}
	fmt.Fprintln(w)

	if len(dm.Edges) == 0 {
		fmt.Fprintln(w, "  No cross-service dependencies found in trace data.")
		return
	}

	// ASCII graph
	fmt.Fprintln(w, output.AccentStyle.Render("  Dependency Graph"))
	fmt.Fprintln(w)
	renderASCIIGraph(w, dm)
	fmt.Fprintln(w)

	// Edge table
	fmt.Fprintln(w, output.AccentStyle.Render("  Edge Details"))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-25s %-5s %-25s %8s %8s %8s %10s\n",
		"FROM", "", "TO", "CALLS", "ERRORS", "ERR%", "AVG(ms)")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 95))

	for _, edge := range dm.Edges {
		errStyle := ""
		if edge.ErrorRate > 10 {
			errStyle = "!"
		}
		fmt.Fprintf(w, "  %-25s  →   %-25s %8d %8d %7.1f%% %9.1f %s\n",
			edge.From, edge.To, edge.Calls, edge.Errors, edge.ErrorRate, edge.AvgLatency, errStyle)
	}
	fmt.Fprintln(w)

	if dm.AISummary != "" {
		fmt.Fprintln(w, output.AccentStyle.Render("━━━ AI Analysis ━━━"))
		fmt.Fprintln(w)
		fmt.Fprintln(w, dm.AISummary)
	}
}

// RenderMarkdown outputs the dependency map as markdown.
func RenderMarkdown(w io.Writer, dm *DependencyMap) {
	fmt.Fprintf(w, "# Service Dependency Map\n\n")
	fmt.Fprintf(w, "**Generated:** %s  \n", dm.GeneratedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "**Duration:** %d minutes  \n", dm.Duration)
	fmt.Fprintf(w, "**Services:** %d | **Edges:** %d\n\n", len(dm.Nodes), len(dm.Edges))

	// Mermaid diagram
	fmt.Fprintf(w, "## Dependency Graph\n\n")
	fmt.Fprintf(w, "```mermaid\ngraph LR\n")
	for _, edge := range dm.Edges {
		label := fmt.Sprintf("%d calls", edge.Calls)
		if edge.ErrorRate > 5 {
			label += fmt.Sprintf(" (%.0f%% err)", edge.ErrorRate)
		}
		fromID := sanitizeMermaidID(edge.From)
		toID := sanitizeMermaidID(edge.To)
		fmt.Fprintf(w, "    %s[%s] -->|%s| %s[%s]\n", fromID, edge.From, label, toID, edge.To)
	}
	fmt.Fprintf(w, "```\n\n")

	// Edge table
	fmt.Fprintf(w, "## Edge Details\n\n")
	fmt.Fprintf(w, "| From | To | Calls | Errors | Error Rate | Avg Latency |\n")
	fmt.Fprintf(w, "|------|-----|-------|--------|------------|-------------|\n")
	for _, edge := range dm.Edges {
		fmt.Fprintf(w, "| %s | %s | %d | %d | %.1f%% | %.1fms |\n",
			edge.From, edge.To, edge.Calls, edge.Errors, edge.ErrorRate, edge.AvgLatency)
	}
	fmt.Fprintln(w)

	if dm.AISummary != "" {
		fmt.Fprintf(w, "## AI Analysis\n\n%s\n", dm.AISummary)
	}
}

func renderASCIIGraph(w io.Writer, dm *DependencyMap) {
	// Group by source service
	bySource := make(map[string][]Edge)
	for _, edge := range dm.Edges {
		bySource[edge.From] = append(bySource[edge.From], edge)
	}

	sources := make([]string, 0, len(bySource))
	for s := range bySource {
		sources = append(sources, s)
	}
	sort.Strings(sources)

	for _, src := range sources {
		edges := bySource[src]
		node := dm.Nodes[src]
		prefix := "  "
		if node != nil && node.IsRoot {
			prefix = "▶ "
		}
		fmt.Fprintf(w, "  %s%s\n", prefix, output.AccentStyle.Render(src))
		for i, edge := range edges {
			connector := "├──"
			if i == len(edges)-1 {
				connector = "└──"
			}
			errTag := ""
			if edge.ErrorRate > 5 {
				errTag = output.ErrorStyle.Render(fmt.Sprintf(" [%.0f%% errors]", edge.ErrorRate))
			}
			fmt.Fprintf(w, "  %s  %s → %s (%d calls, %.1fms avg)%s\n",
				"  ", connector, edge.To, edge.Calls, edge.AvgLatency, errTag)
		}
	}
}

func sanitizeMermaidID(name string) string {
	r := strings.NewReplacer("-", "_", ".", "_", "/", "_", " ", "_")
	return r.Replace(name)
}

func appendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}

func buildAIPrompt(dm *DependencyMap) string {
	var sb strings.Builder
	sb.WriteString("Analyze this service dependency map from a Signoz observability instance.\n\n")
	sb.WriteString(fmt.Sprintf("Time window: last %d minutes\n", dm.Duration))
	sb.WriteString(fmt.Sprintf("Services: %d, Edges: %d\n\n", len(dm.Nodes), len(dm.Edges)))

	sb.WriteString("Dependencies (from → to):\n")
	for _, edge := range dm.Edges {
		sb.WriteString(fmt.Sprintf("  %s → %s: %d calls, %d errors (%.1f%%), avg %.1fms\n",
			edge.From, edge.To, edge.Calls, edge.Errors, edge.ErrorRate, edge.AvgLatency))
	}

	sb.WriteString("\nProvide:\n")
	sb.WriteString("1. Architecture overview — what does this system look like?\n")
	sb.WriteString("2. Critical paths — which dependency chains are most important?\n")
	sb.WriteString("3. Risk areas — high error rates, single points of failure, tight coupling\n")
	sb.WriteString("4. Recommendations — what should be improved?\n")
	sb.WriteString("\nBe concise and actionable.\n")

	return sb.String()
}
