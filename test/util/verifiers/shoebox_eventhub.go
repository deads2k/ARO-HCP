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

package verifiers

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
)

type verifyShoeboxEventHubImpl struct {
	connectionString string
	eventHubName     string
}

// diagnosticRecord represents a single record within the Azure Monitor
// diagnostic settings Event Hub envelope.
type diagnosticRecord struct {
	Time          string `json:"time"`
	ResourceID    string `json:"resourceId"`
	Category      string `json:"category"`
	Level         string `json:"level"`
	OperationName string `json:"operationName"`
	Location      string `json:"location"`
	Properties    any    `json:"properties"`
}

// diagnosticEnvelope is the JSON structure Azure Monitor sends to Event Hub.
// Each message body contains {"records": [...]}.
type diagnosticEnvelope struct {
	Records []diagnosticRecord `json:"records"`
}

func (v verifyShoeboxEventHubImpl) Name() string {
	return "VerifyShoeboxEventHub"
}

func (v verifyShoeboxEventHubImpl) Verify(ctx context.Context) error {
	consumerClient, err := azeventhubs.NewConsumerClientFromConnectionString(
		v.connectionString, v.eventHubName, azeventhubs.DefaultConsumerGroup, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create Event Hub consumer client: %w", err)
	}
	defer consumerClient.Close(ctx)

	ehProps, err := consumerClient.GetEventHubProperties(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get Event Hub properties: %w", err)
	}

	if len(ehProps.PartitionIDs) == 0 {
		return fmt.Errorf("event hub %s has no partitions", v.eventHubName)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []diagnosticRecord
		errors  []error
	)

	for _, partitionID := range ehProps.PartitionIDs {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			records, err := v.receiveFromPartition(ctx, consumerClient, pid)
			mu.Lock()
			if err != nil {
				errors = append(errors, err)
			}
			if len(records) > 0 {
				results = append(results, records...)
			}
			mu.Unlock()
		}(partitionID)
	}
	wg.Wait()

	if len(errors) > 0 {
		return fmt.Errorf("failed to read from Event Hub partitions: %v", errors)
	}

	if len(results) == 0 {
		return &retryableError{err: fmt.Errorf("no shoebox log records found in any Event Hub partition")}
	}

	return nil
}

func (v verifyShoeboxEventHubImpl) receiveFromPartition(
	ctx context.Context,
	client *azeventhubs.ConsumerClient,
	partitionID string,
) ([]diagnosticRecord, error) {
	partitionClient, err := client.NewPartitionClient(partitionID, &azeventhubs.PartitionClientOptions{
		StartPosition: azeventhubs.StartPosition{
			Earliest: ptr.To(true),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create partition client for partition %s: %w", partitionID, err)
	}
	defer partitionClient.Close(ctx)

	receiveCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	events, err := partitionClient.ReceiveEvents(receiveCtx, 100, nil)
	if err != nil && receiveCtx.Err() == nil {
		return nil, fmt.Errorf("failed to receive events from partition %s: %w", partitionID, err)
	}

	var allRecords []diagnosticRecord
	for _, event := range events {
		var envelope diagnosticEnvelope
		if err := json.Unmarshal(event.Body, &envelope); err != nil {
			continue
		}
		for _, record := range envelope.Records {
			if isValidShoeboxRecord(record) {
				allRecords = append(allRecords, record)
			}
		}
	}
	return allRecords, nil
}

func isValidShoeboxRecord(r diagnosticRecord) bool {
	return r.Time != "" &&
		r.ResourceID != "" &&
		r.Category != "" &&
		r.Level != "" &&
		r.OperationName != "" &&
		r.Location != "" &&
		r.Properties != nil
}

// VerifyShoeboxEventHub creates a verifier that consumes messages from an
// Azure Event Hub and checks that at least one message contains valid
// shoebox diagnostic log records.
func VerifyShoeboxEventHub(connectionString, eventHubName string) verifyShoeboxEventHubImpl {
	return verifyShoeboxEventHubImpl{
		connectionString: connectionString,
		eventHubName:     eventHubName,
	}
}
