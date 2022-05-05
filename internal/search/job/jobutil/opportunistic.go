package jobutil

import (
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
	for _, b := range plan {
		for _, newBasic := range BuildBasic(b) {
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
	bs := []query.Basic{b} // Include incoming query.
	if g := UnorderedPatterns(b); g != nil {
		bs = append(bs, *g)
	}
	return bs
}

// UnorderedPatterns generates a query that interprets all recognized patterns
// as unordered terms (`and`-ed terms). Brittle assumption: only for queries in
// default/literal mode, where all terms are space-separated and spaces are
// unescapable, implying we can obtain patterns with a split on space.
func UnorderedPatterns(b query.Basic) *query.Basic {
	var andPatterns []query.Node
	query.VisitPattern([]query.Node{b.Pattern}, func(value string, negated bool, annotation query.Annotation) {
		if negated {
			// append negated terms as-is.
			andPatterns = append(andPatterns, query.Pattern{
				Value:      value,
				Negated:    negated,
				Annotation: annotation,
			})
			return
		}
		for _, p := range strings.Split(value, " ") {
			andPatterns = append(andPatterns, query.NewPattern(p, query.Literal, query.Range{}))
		}
	})
	return &query.Basic{
		Parameters: b.Parameters,
		Pattern:    query.Operator{Kind: query.And, Operands: andPatterns, Annotation: query.Annotation{}},
	}

	/*
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
	*/
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
