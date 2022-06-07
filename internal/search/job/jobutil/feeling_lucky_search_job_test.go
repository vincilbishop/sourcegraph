package jobutil

import (
	"testing"

	"github.com/hexops/autogold"
	"github.com/sourcegraph/sourcegraph/internal/search"
	"github.com/sourcegraph/sourcegraph/internal/search/query"
	"github.com/sourcegraph/sourcegraph/internal/search/run"
	"github.com/sourcegraph/sourcegraph/schema"
)

func TestNewFeelingLuckySearchJob(t *testing.T) {
	test := func(q string) string {
		inputs := &run.SearchInputs{
			UserSettings: &schema.Settings{},
			Protocol:     search.Streaming,
			PatternType:  query.SearchTypeLucky,
		}
		plan, _ := query.Pipeline(query.InitLiteral(q))
		job, _ := NewPlanJob(inputs, plan)
		return PrettyJSONVerbose(job)
	}

	t.Run("trigger unquoted rule", func(t *testing.T) {
		autogold.Equal(t, autogold.Raw(test(`"lucky"`)))
	})

	t.Run("trigger unordered patterns", func(t *testing.T) {
		autogold.Equal(t, autogold.Raw(test(`context:global parse func`)))
	})
}

func TestUriAsFilter(t *testing.T) {
	test := func(q string) string {
		plan, _ := query.Pipeline(query.InitLiteral(q))
		b := uriAsFilter(plan[0])
		if b == nil {
			return "nothing"
		}
		return b.StringHuman()
	}

	t.Run("uri to repo rule", func(t *testing.T) {
		autogold.Equal(t, autogold.Raw(test(`https://github.com/sourcegraph/sourcegraph/blob/main/cmd/loadtest/build.sh#13`)))
	})
}
