package jobutil

import (
	"regexp"
	"strings"

	"github.com/sourcegraph/sourcegraph/internal/search"
	"github.com/sourcegraph/sourcegraph/internal/search/filter"
	"github.com/sourcegraph/sourcegraph/internal/search/job"
	"github.com/sourcegraph/sourcegraph/internal/search/query"
	"github.com/sourcegraph/sourcegraph/internal/search/run"
)

// NewOpportunisticJob generates an opportunistic search query by applying various rules on
// the input string.
func NewOpportunisticJob(inputs *run.SearchInputs, plan query.Plan) job.Job {
	children := make([]job.Job, 0, len(plan))
	for _, q := range plan {
		for _, newBasic := range BuildBasic(q) {
			child, err := ToEvaluateJob(inputs, newBasic)
			if err != nil {
				panic("generated an invalid basic query D:")
			}

			child, err = OptimizationPass(child, inputs, newBasic)
			if err != nil {
				panic("optimization pass on generated query failed D:")
			}

			// Apply selectors
			if v, _ := newBasic.ToParseTree().StringValue(query.FieldSelect); v != "" {
				sp, _ := filter.SelectPathFromString(v) // Invariant: select already validated
				child = NewSelectJob(sp, child)
			}

			// Apply limits and Timeouts.
			maxResults := newBasic.ToParseTree().MaxResults(inputs.DefaultLimit())
			timeout := search.TimeoutDuration(newBasic)
			child = NewTimeoutJob(timeout, NewLimitJob(maxResults, child))

			children = append(children, child)
		}
	}
	return NewOrJob(children...)
}

func BuildBasic(b query.Basic) []query.Basic {
	var bs []query.Basic
	if g := OnlySelect(b); g != nil {
		bs = append(bs, *g)
	}
	return bs
}

func OnlySelect(b query.Basic) *query.Basic {
	s := query.StringHuman([]query.Node{b.Pattern})
	r := regexp.MustCompile(`\bonly (repos?|files?|paths?|content|symbols?)\b`)
	out := r.Split(s, -1)
	if len(out) == 0 {
		return nil
	}

	// remove any selects
	parameters := query.MapField(
		ParametersToNodes(b.Parameters),
		query.FieldSelect,
		func(_ string, _ bool, _ query.Annotation) query.Node {
			return nil
		})

	vr := regexp.MustCompile(`repo|file|path|content|symbol`)
	stripped := strings.Join(out, "")
	parameters = append(parameters, query.Parameter{Field: "select", Value: vr.FindString(r.FindString(s))})

	return &query.Basic{
		Parameters: NodesToParameters(parameters),
		Pattern:    query.Pattern{Value: strings.TrimSpace(stripped)},
	}
}

func ParametersToNodes(parameters []query.Parameter) []query.Node {
	var nodes []query.Node
	for _, n := range parameters {
		nodes = append(nodes, query.Node(n))
	}
	return nodes
}

func NodesToParameters(nodes []query.Node) []query.Parameter {
	b, _ := query.ToBasicQuery(nodes)
	return b.Parameters
}

/*
func Run(ctx context.Context, clients RuntimeClients, stream streaming.Sender) (*search.Alert, error) {
	return nil, nil
}
*/
