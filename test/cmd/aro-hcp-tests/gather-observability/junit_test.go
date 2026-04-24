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
	"encoding/xml"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

func TestBuildTestName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		identity alertIdentity
		want     string
	}{
		{
			name: "no_labels",
			identity: alertIdentity{
				Name:   "MiseEnvoyScrapeDown",
				Labels: map[string]string{},
			},
			want: "[aro-hcp-observability] alert MiseEnvoyScrapeDown should not have fired",
		},
		{
			name: "one_label",
			identity: alertIdentity{
				Name:   "BackendControllerRetryHotLoop",
				Labels: map[string]string{"name": "operationnodepoolcreate"},
			},
			want: `[aro-hcp-observability] alert BackendControllerRetryHotLoop{name="operationnodepoolcreate"} should not have fired`,
		},
		{
			name: "multiple_labels_sorted",
			identity: alertIdentity{
				Name:   "SomeAlert",
				Labels: map[string]string{"zone": "us-east-1", "name": "test", "app": "frontend"},
			},
			want: `[aro-hcp-observability] alert SomeAlert{app="frontend", name="test", zone="us-east-1"} should not have fired`,
		},
		{
			name: "nil_labels",
			identity: alertIdentity{
				Name:   "KubePodImagePull",
				Labels: nil,
			},
			want: "[aro-hcp-observability] alert KubePodImagePull should not have fired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildTestName(tt.identity)
			if got != tt.want {
				t.Errorf("buildTestName() =\n  %q\nwant:\n  %q", got, tt.want)
			}
		})
	}
}

func mustTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func TestAlertsToJUnit(t *testing.T) {
	t.Parallel()

	tw := timing.TimeWindow{
		Start: *mustTime("2026-04-13T06:00:00Z"),
		End:   *mustTime("2026-04-13T08:00:00Z"),
	}

	alerts := []alert{
		{
			Alert: alertData{
				Name:      "BackendControllerRetryHotLoop",
				Severity:  armalertsmanagement.SeveritySev3,
				Condition: "Fired",
				StartsAt:  mustTime("2026-04-13T06:10:00Z"),
				EndsAt:    mustTime("2026-04-13T06:53:23Z"),
				Labels: map[string]string{
					"alertname": "BackendControllerRetryHotLoop",
					"name":      "operationnodepoolcreate",
					"severity":  "warning",
					"cluster":   "prow-j3151872-svc",
					"namespace": "aro-hcp",
				},
				Description: "Backend controller workqueue operationnodepoolcreate has a retry ratio of > 50%",
			},
			Metadata: alertMetadata{
				KnownIssue:       true,
				KnownIssueReason: "Nodepool create controller retry hot loops are observed during e2e runs. Needs investigation.",
			},
		},
		{
			Alert: alertData{
				Name:      "MiseEnvoyScrapeDown",
				Severity:  armalertsmanagement.SeveritySev3,
				Condition: "Resolved",
				StartsAt:  mustTime("2026-04-13T06:20:00Z"),
				EndsAt:    mustTime("2026-04-13T06:30:00Z"),
				Labels: map[string]string{
					"alertname": "MiseEnvoyScrapeDown",
					"severity":  "warning",
					"cluster":   "prow-j3151872-svc",
					"namespace": "aro-hcp",
				},
				Description: "Mise Envoy scrape target is down",
			},
			Metadata: alertMetadata{
				KnownIssue:       true,
				KnownIssueReason: "Mise Envoy scrape targets intermittently go down during e2e runs.",
			},
		},
		{
			Alert: alertData{
				Name:      "KubePodImagePull",
				Severity:  armalertsmanagement.SeveritySev4,
				Condition: "Fired",
				StartsAt:  mustTime("2026-04-13T07:00:00Z"),
				Labels: map[string]string{
					"alertname": "KubePodImagePull",
					"severity":  "warning",
					"cluster":   "prow-j3151872-svc",
					"pod":       "frontend-abc123",
					"namespace": "aro-hcp",
				},
			},
			Metadata: alertMetadata{
				KnownIssue: false,
			},
		},
	}

	suites := alertsToJUnit(alerts, tw)
	xmlBytes, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal JUnit XML: %v", err)
	}
	CompareWithFixture(t, string(xmlBytes), WithExtension(".xml"))
}

func TestAlertsToJUnitMixedGroup(t *testing.T) {
	t.Parallel()

	tw := timing.TimeWindow{
		Start: *mustTime("2026-04-13T06:00:00Z"),
		End:   *mustTime("2026-04-13T08:00:00Z"),
	}

	alerts := []alert{
		{
			Alert: alertData{
				Name:      "BackendControllerRetryHotLoop",
				Severity:  armalertsmanagement.SeveritySev3,
				Condition: "Fired",
				StartsAt:  mustTime("2026-04-13T06:10:00Z"),
				EndsAt:    mustTime("2026-04-13T06:30:00Z"),
				Labels: map[string]string{
					"alertname": "BackendControllerRetryHotLoop",
					"name":      "operationnodepoolcreate",
					"severity":  "warning",
					"cluster":   "prow-j3151872-svc",
				},
				Description: "Backend controller retry hot loop (known firing)",
			},
			Metadata: alertMetadata{
				KnownIssue:       true,
				KnownIssueReason: "Known issue: hot loop during provisioning.",
			},
		},
		{
			Alert: alertData{
				Name:      "BackendControllerRetryHotLoop",
				Severity:  armalertsmanagement.SeveritySev3,
				Condition: "Fired",
				StartsAt:  mustTime("2026-04-13T07:00:00Z"),
				EndsAt:    mustTime("2026-04-13T07:15:00Z"),
				Labels: map[string]string{
					"alertname": "BackendControllerRetryHotLoop",
					"name":      "operationnodepoolcreate",
					"severity":  "warning",
					"cluster":   "prow-j3151872-svc",
				},
				Description: "Backend controller retry hot loop (unknown firing)",
			},
			Metadata: alertMetadata{
				KnownIssue: false,
			},
		},
	}

	suites := alertsToJUnit(alerts, tw)
	xmlBytes, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal JUnit XML: %v", err)
	}
	CompareWithFixture(t, string(xmlBytes), WithExtension(".xml"))
}
