package jobutil

import (
	"testing"

	"github.com/hexops/autogold"
	"github.com/sourcegraph/sourcegraph/internal/search/query"
)

/*

func TestNewOpportunisticJob(t *testing.T) {
	test := func(input string) string {
		plan, _ := query.Pipeline(query.InitLiteral(input))
		inputs := &run.SearchInputs{
			UserSettings:        &schema.Settings{},
			PatternType:         query.SearchTypeLiteral,
			Protocol:            search.Streaming,
			OnSourcegraphDotCom: true,
		}
		oppoJob := NewOpportunisticJob(inputs, plan)
		return PrettyJSONVerbose(oppoJob)
	}

	autogold.Want("select rule", ``).Equal(t, test("repo:sourcegraph func parse only repo"))
	// only repo(func parse) -> invalid basic query
	// regexp mode concat messes up the heuristic: `func.*parse.*only.*repo`
	// also ar distribution: `(func parse or test) only files`
}
*/

func TestPatternsAsRepoPaths(t *testing.T) {
	test := func(input string) string {
		plan, _ := query.Pipeline(query.InitLiteral(input))
		basic := plan[0]
		newBasic := PatternsAsRepoPaths(basic)
		if newBasic == nil {
			return "generated query is nil--something is invalid"
		}
		return newBasic.StringHuman()
	}
	autogold.Want("URL pattern as repo paths", "file:foo repo:yes/repo repo:also/repo not/repo pattern").
		Equal(t, test("https://github.com/yes/repo not/repo github.com/also/repo file:foo pattern"))
}

func TestUnquotedPatterns(t *testing.T) {
	test := func(input string) string {
		plan, _ := query.Pipeline(query.InitLiteral(input))
		basic := plan[0]
		newBasic := UnquotedPatterns(basic)
		if newBasic == nil {
			return "generated query is nil--something is invalid"
		}
		return newBasic.StringHuman()
	}
	autogold.Want("unquoted patterns", ` "result please unless? maybe ok"`).
		Equal(t, test(`"result please" unless? "maybe" ok`))
}
