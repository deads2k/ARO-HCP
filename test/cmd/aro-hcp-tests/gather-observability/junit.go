// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gatherobservability

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"
	"time"

	_ "embed"

	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/junit"
)

//go:embed artifacts/failure-output.tmpl
var failureOutputTmplData string

// ephemeralLabels is the set of Prometheus labels stripped from the
// testcase identity. These labels change per Prow job or pod lifecycle,
// making them unsuitable for stable test names in Sippy.
var ephemeralLabels = map[string]bool{
	"alertname":          true,
	"severity":           true,
	"cluster":            true,
	"pod":                true,
	"instance":           true,
	"container":          true,
	"endpoint":           true,
	"job":                true,
	"node":               true,
	"replica":            true,
	"prometheus_replica": true,
	"prometheus":         true,
	"service":            true,
	"uid":                true,
	"namespace":          true,
}

type alertIdentity struct {
	Name   string
	Labels map[string]string
}

func stableLabels(labels map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range labels {
		if !ephemeralLabels[k] {
			out[k] = v
		}
	}
	return out
}

func identityFromAlert(a alert) alertIdentity {
	return alertIdentity{
		Name:   a.Alert.Name,
		Labels: stableLabels(a.Alert.Labels),
	}
}

func groupAlerts(alerts []alert) map[string][]alert {
	groups := make(map[string][]alert)
	for _, a := range alerts {
		id := identityFromAlert(a)
		key := buildTestName(id)
		groups[key] = append(groups[key], a)
	}
	return groups
}

func buildTestName(id alertIdentity) string {
	var sb strings.Builder
	sb.WriteString("[aro-hcp-observability] alert ")
	sb.WriteString(id.Name)

	keys := sortedKeys(id.Labels)
	if len(keys) > 0 {
		sb.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%s=%q", k, id.Labels[k])
		}
		sb.WriteString("}")
	}

	sb.WriteString(" should not have fired")
	return sb.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func alertsToJUnit(alerts []alert, timeWindow timing.TimeWindow) *junit.TestSuites {
	groups := groupAlerts(alerts)

	testNames := make([]string, 0, len(groups))
	for name := range groups {
		testNames = append(testNames, name)
	}
	sort.Strings(testNames)

	var testCases []*junit.TestCase
	var totalDuration float64
	var numFailed, numSkipped uint

	for _, testName := range testNames {
		firings := groups[testName]
		duration := computeGroupDuration(firings, timeWindow)
		totalDuration += duration

		tc := &junit.TestCase{
			Name:     testName,
			Duration: duration,
		}

		allKnown := true
		for _, f := range firings {
			if !f.Metadata.KnownIssue {
				allKnown = false
				break
			}
		}

		if allKnown {
			numSkipped++
			tc.SkipMessage = &junit.SkipMessage{
				Message: buildSkipMessage(firings),
			}
		} else {
			numFailed++
			tc.FailureOutput = &junit.FailureOutput{
				Message: buildFailureMessage(firings),
				Output:  buildFailureOutput(firings),
			}
		}

		testCases = append(testCases, tc)
	}

	return &junit.TestSuites{
		Suites: []*junit.TestSuite{
			{
				Name:       "aro-hcp-tests",
				NumTests:   uint(len(testCases)),
				NumFailed:  numFailed,
				NumSkipped: numSkipped,
				Duration:   totalDuration,
				TestCases:  testCases,
			},
		},
	}
}

func computeGroupDuration(firings []alert, tw timing.TimeWindow) float64 {
	var total float64
	for _, f := range firings {
		if f.Alert.StartsAt == nil {
			continue
		}
		end := tw.End
		if f.Alert.EndsAt != nil {
			end = *f.Alert.EndsAt
		}
		d := end.Sub(*f.Alert.StartsAt).Seconds()
		if d > 0 {
			total += d
		}
	}
	return total
}

func buildSkipMessage(firings []alert) string {
	reasons := make(map[string]bool)
	var ordered []string
	for _, f := range firings {
		r := f.Metadata.KnownIssueReason
		if r != "" && !reasons[r] {
			reasons[r] = true
			ordered = append(ordered, r)
		}
	}
	return "known issue: " + strings.Join(ordered, "; ")
}

func buildFailureMessage(firings []alert) string {
	var unknown, known int
	for _, f := range firings {
		if f.Metadata.KnownIssue {
			known++
		} else {
			unknown++
		}
	}
	total := len(firings)
	if known == 0 {
		return fmt.Sprintf("alert fired %d time(s)", total)
	}
	return fmt.Sprintf("alert fired %d time(s) (%d unknown, %d known)", total, unknown, known)
}

var failureOutputTmpl = template.Must(template.New("failure-output.tmpl").Funcs(template.FuncMap{
	"state": func(condition string) string {
		if condition == "Fired" {
			return "Fired (not resolved)"
		}
		return condition
	},
	"formatTime": func(t interface{}) string {
		if t == nil {
			return ""
		}
		if v, ok := t.(*time.Time); ok && v != nil {
			return v.UTC().Format("2006-01-02T15:04:05Z")
		}
		return ""
	},
	"formatLabels": func(labels map[string]string) string {
		keys := sortedKeys(labels)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s=%q", k, labels[k])
		}
		return strings.Join(parts, ", ")
	},
	"inc": func(i int) int { return i + 1 },
}).Parse(failureOutputTmplData))

func buildFailureOutput(firings []alert) string {
	var unknown, known []alert
	for _, f := range firings {
		if f.Metadata.KnownIssue {
			known = append(known, f)
		} else {
			unknown = append(unknown, f)
		}
	}
	var buf bytes.Buffer
	_ = failureOutputTmpl.Execute(&buf, struct {
		Unknown []alert
		Known   []alert
	}{Unknown: unknown, Known: known})
	return buf.String()
}
