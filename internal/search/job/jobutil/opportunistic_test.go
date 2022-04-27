package jobutil

import (
	"testing"

	"github.com/hexops/autogold"
	"github.com/sourcegraph/sourcegraph/internal/search"
	"github.com/sourcegraph/sourcegraph/internal/search/query"
	"github.com/sourcegraph/sourcegraph/internal/search/run"
	"github.com/sourcegraph/sourcegraph/schema"
)

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

	autogold.Want("select rule", `{
  "TIMEOUT": {
    "LIMIT": {
      "SELECT": {
        "PARALLEL": [
          {
            "REPOPAGER": {
              "PARALLEL": [
                {
                  "ZoektRepoSubset": {
                    "Repos": null,
                    "Query": {
                      "Pattern": "func parse",
                      "CaseSensitive": false,
                      "FileName": false,
                      "Content": false
                    },
                    "Typ": "text",
                    "FileMatchLimit": 500,
                    "Select": [
                      "repo"
                    ]
                  }
                },
                {
                  "Searcher": {
                    "PatternInfo": {
                      "Pattern": "func parse",
                      "IsNegated": false,
                      "IsRegExp": false,
                      "IsStructuralPat": false,
                      "CombyRule": "",
                      "IsWordMatch": false,
                      "IsCaseSensitive": false,
                      "FileMatchLimit": 500,
                      "Index": "yes",
                      "Select": [
                        "repo"
                      ],
                      "IncludePatterns": null,
                      "ExcludePattern": "",
                      "FilePatternsReposMustInclude": null,
                      "FilePatternsReposMustExclude": null,
                      "PathPatternsAreCaseSensitive": false,
                      "PatternMatchesContent": true,
                      "PatternMatchesPath": true,
                      "Languages": null
                    },
                    "Repos": null,
                    "Indexed": false,
                    "UseFullDeadline": true
                  }
                }
              ]
            }
          },
          {
            "RepoSearch": {
              "RepoOptions": {
                "RepoFilters": [
                  "sourcegraph",
                  "func parse"
                ],
                "MinusRepoFilters": null,
                "Dependencies": null,
                "CaseSensitiveRepoFilters": false,
                "SearchContextSpec": "",
                "CommitAfter": "",
                "Visibility": "Any",
                "Limit": 0,
                "Cursors": null,
                "ForkSet": false,
                "NoForks": true,
                "OnlyForks": false,
                "ArchivedSet": false,
                "NoArchived": true,
                "OnlyArchived": false
              },
              "FilePatternsReposMustInclude": null,
              "FilePatternsReposMustExclude": null,
              "Features": {
                "ContentBasedLangFilters": false
              },
              "Mode": 0
            }
          },
          {
            "ComputeExcludedRepos": {
              "Options": {
                "RepoFilters": [
                  "sourcegraph"
                ],
                "MinusRepoFilters": null,
                "Dependencies": null,
                "CaseSensitiveRepoFilters": false,
                "SearchContextSpec": "",
                "CommitAfter": "",
                "Visibility": "Any",
                "Limit": 0,
                "Cursors": null,
                "ForkSet": false,
                "NoForks": true,
                "OnlyForks": false,
                "ArchivedSet": false,
                "NoArchived": true,
                "OnlyArchived": false
              }
            }
          }
        ]
      },
      "value": "repo"
    },
    "value": 500
  },
  "value": "20s"
}`).Equal(t, test("repo:sourcegraph func parse only repo"))
	// only repo(func parse) -> invalid basic query
	// regexp mode concat messes up the heuristic: `func.*parse.*only.*repo`
	// also ar distribution: `(func parse or test) only files`
}
