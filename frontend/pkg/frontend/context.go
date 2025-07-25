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

package frontend

import (
	"context"
	"fmt"
	"log/slog"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/frontend/pkg/util"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type ContextError struct {
	got any
	key contextKey
}

func (c *ContextError) Error() string {
	return fmt.Sprintf(
		"error retrieving value for key %q from context, value obtained was '%v' and type obtained was '%T'",
		c.key,
		c.got,
		c.got)
}

type contextKey int

func (c contextKey) String() string {
	switch c {
	case contextKeyOriginalPath:
		return "originalPath"
	case contextKeyBody:
		return "body"
	case contextKeyAuditLogger:
		return "auditLogger"
	case contextKeyLogger:
		return "logger"
	case contextKeyVersion:
		return "version"
	case contextKeyDBClient:
		return "dbClient"
	case contextKeyResourceID:
		return "resourceID"
	case contextKeyCorrelationData:
		return "correlationData"
	case contextKeySystemData:
		return "systemData"
	case contextKeyPattern:
		return "pattern"
	}
	return "<unknown>"
}

const (
	// Keys for request-scoped data in http.Request contexts
	contextKeyOriginalPath contextKey = iota
	contextKeyBody
	contextKeyAuditLogger
	contextKeyLogger
	contextKeyVersion
	contextKeyDBClient
	contextKeyResourceID
	contextKeyCorrelationData
	contextKeySystemData
	contextKeyPattern
)

func ContextWithOriginalPath(ctx context.Context, originalPath string) context.Context {
	return context.WithValue(ctx, contextKeyOriginalPath, originalPath)
}

func OriginalPathFromContext(ctx context.Context) (string, error) {
	originalPath, ok := ctx.Value(contextKeyOriginalPath).(string)
	if !ok {
		err := &ContextError{
			got: originalPath,
			key: contextKeyOriginalPath,
		}
		return originalPath, err
	}
	return originalPath, nil
}

func ContextWithBody(ctx context.Context, body []byte) context.Context {
	return context.WithValue(ctx, contextKeyBody, body)
}

func BodyFromContext(ctx context.Context) ([]byte, error) {
	body, ok := ctx.Value(contextKeyBody).([]byte)
	if !ok {
		err := &ContextError{
			got: body,
			key: contextKeyBody,
		}
		return body, err
	}
	return body, nil
}

func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKeyLogger, logger)
}

func LoggerFromContext(ctx context.Context) *slog.Logger {
	logger, ok := ctx.Value(contextKeyLogger).(*slog.Logger)
	if !ok {
		err := &ContextError{
			got: logger,
			key: contextKeyLogger,
		}
		// Return the default logger as a fail-safe, but log
		// the failure to obtain the logger from the context.
		logger = util.DefaultLogger()
		logger.Error(err.Error())
	}
	return logger
}

func ContextWithVersion(ctx context.Context, version api.Version) context.Context {
	return context.WithValue(ctx, contextKeyVersion, version)
}

func VersionFromContext(ctx context.Context) (api.Version, error) {
	version, ok := ctx.Value(contextKeyVersion).(api.Version)
	if !ok {
		err := &ContextError{
			got: version,
			key: contextKeyVersion,
		}
		return version, err
	}
	return version, nil
}

func ContextWithResourceID(ctx context.Context, resourceID *azcorearm.ResourceID) context.Context {
	return context.WithValue(ctx, contextKeyResourceID, resourceID)
}

func ResourceIDFromContext(ctx context.Context) (*azcorearm.ResourceID, error) {
	resourceID, ok := ctx.Value(contextKeyResourceID).(*azcorearm.ResourceID)
	if !ok {
		err := &ContextError{
			got: resourceID,
			key: contextKeyResourceID,
		}
		return resourceID, err
	}
	return resourceID, nil
}

func ContextWithCorrelationData(ctx context.Context, correlationData *arm.CorrelationData) context.Context {
	return context.WithValue(ctx, contextKeyCorrelationData, correlationData)
}

func CorrelationDataFromContext(ctx context.Context) (*arm.CorrelationData, error) {
	correlationData, ok := ctx.Value(contextKeyCorrelationData).(*arm.CorrelationData)
	if !ok {
		err := &ContextError{
			got: correlationData,
			key: contextKeyCorrelationData,
		}
		return correlationData, err
	}
	return correlationData, nil
}

func ContextWithSystemData(ctx context.Context, systemData *arm.SystemData) context.Context {
	return context.WithValue(ctx, contextKeySystemData, systemData)
}

func SystemDataFromContext(ctx context.Context) (*arm.SystemData, error) {
	systemData, ok := ctx.Value(contextKeySystemData).(*arm.SystemData)
	if !ok {
		err := &ContextError{
			got: systemData,
			key: contextKeySystemData,
		}
		return systemData, err
	}
	return systemData, nil
}

func ContextWithPattern(ctx context.Context, pattern *string) context.Context {
	return context.WithValue(ctx, contextKeyPattern, pattern)
}

func PatternFromContext(ctx context.Context) *string {
	pattern, _ := ctx.Value(contextKeyPattern).(*string)
	return pattern
}
