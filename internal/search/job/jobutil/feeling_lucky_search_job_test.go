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
		}
		plan, _ := query.Pipeline(query.InitLiteral(q))
		job, _ := NewPlanJob(inputs, plan)
		return PrettyJSONVerbose(job)
	}

	autogold.Want("default rules", ``).Equal(t, test(`"lucky"`))
}
