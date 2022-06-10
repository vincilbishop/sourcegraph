package cliutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	descriptions "github.com/sourcegraph/sourcegraph/internal/database/migration/schemas"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

type ExpectedSchemaFactory func(repoName, version string) (descriptions.SchemaDescription, bool, error)

// TODO - document
func GCSExpectedSchemaFactory(filename, version string) (schemaDescription descriptions.SchemaDescription, _ bool, _ error) {
	return fetchSchema(fmt.Sprintf("https://storage.googleapis.com/sourcegraph-assets/migrations/drift/%s-%s.sql", version, url.QueryEscape(filename)))
}

// TODO - document
func GitHubExpectedSchemaFactory(filename, version string) (descriptions.SchemaDescription, bool, error) {
	if !regexp.MustCompile(`(^v\d+\.\d+\.\d+$)|(^[A-Fa-f0-9]{40}$)`).MatchString(version) {
		return descriptions.SchemaDescription{}, false, errors.Newf("failed to parse %q - expected a version of the form `vX.Y.Z` or a 40-character commit hash", version)
	}

	return fetchSchema(fmt.Sprintf("https://raw.githubusercontent.com/sourcegraph/sourcegraph/%s/%s", version, filename))
}

// TODO - document
func fetchSchema(url string) (schemaDescription descriptions.SchemaDescription, _ bool, _ error) {
	resp, err := http.Get(url)
	if err != nil {
		return descriptions.SchemaDescription{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return descriptions.SchemaDescription{}, false, nil
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return descriptions.SchemaDescription{}, false, errors.Newf("unexpected status %d from %s: %s", resp.StatusCode, url, body)
	}

	if err := json.NewDecoder(resp.Body).Decode(&schemaDescription); err != nil {
		return descriptions.SchemaDescription{}, false, err
	}

	return schemaDescription, true, err
}
