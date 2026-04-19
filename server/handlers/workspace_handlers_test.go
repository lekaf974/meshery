package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meshery/meshery/server/models"
	"github.com/meshery/meshkit/database"
	"github.com/meshery/meshkit/logger"
	"github.com/meshery/schemas/models/v1beta1/workspace"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newInMemoryWorkspaceProvider creates a DefaultLocalProvider backed by an
// in-memory SQLite database with the workspace schema already migrated.
// This mimics the DefaultLocalProvider while keeping tests self-contained.
func newInMemoryWorkspaceProvider(t *testing.T) *models.DefaultLocalProvider {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory database: %v", err)
	}

	if err := db.AutoMigrate(&workspace.Workspace{}); err != nil {
		t.Fatalf("failed to migrate workspace schema: %v", err)
	}

	dbHandler := &database.Handler{DB: db}

	provider := &models.DefaultLocalProvider{}
	provider.Initialize()
	provider.WorkspacePersister = &models.WorkspacePersister{DB: dbHandler}

	return provider
}

// failingWorkspaceProvider wraps DefaultLocalProvider and overrides SaveWorkspace
// to always return an error, simulating a database or upstream failure.
type failingWorkspaceProvider struct {
	*models.DefaultLocalProvider
}

func (f *failingWorkspaceProvider) SaveWorkspace(_ *http.Request, _ *workspace.WorkspacePayload, _ string, _ bool) ([]byte, error) {
	return nil, errors.New("simulated provider failure")
}

func TestSaveWorkspaceHandler(t *testing.T) {
	log, err := logger.New("test", logger.Options{})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	tests := []struct {
		name           string
		body           string
		setupProvider  func(t *testing.T) models.Provider
		expectedStatus int
		validateBody   func(t *testing.T, body string)
	}{
		{
			name: "given a valid workspace when SaveWorkspaceHandler then return 201",
			body: `{"name":"my-workspace","description":"a test workspace","organization_id":"00000000-0000-0000-0000-000000000001"}`,
			setupProvider: func(t *testing.T) models.Provider {
				return newInMemoryWorkspaceProvider(t)
			},
			expectedStatus: http.StatusCreated,
			validateBody: func(t *testing.T, body string) {
				var ws workspace.Workspace
				if err := json.Unmarshal([]byte(body), &ws); err != nil {
					t.Errorf("response body is not valid workspace JSON: %v", err)
				}
				if ws.Name != "my-workspace" {
					t.Errorf("expected Name %q, got %q", "my-workspace", ws.Name)
				}
				if ws.ID.IsNil() {
					t.Error("expected workspace ID to be set, got nil UUID")
				}
			},
		},
		{
			name: "given a workspace without description when SaveWorkspaceHandler then return 201",
			body: `{"name":"minimal-workspace","organization_id":"00000000-0000-0000-0000-000000000001"}`,
			setupProvider: func(t *testing.T) models.Provider {
				return newInMemoryWorkspaceProvider(t)
			},
			expectedStatus: http.StatusCreated,
			validateBody: func(t *testing.T, body string) {
				var ws workspace.Workspace
				if err := json.Unmarshal([]byte(body), &ws); err != nil {
					t.Errorf("response body is not valid workspace JSON: %v", err)
				}
				if ws.Name != "minimal-workspace" {
					t.Errorf("expected Name %q, got %q", "minimal-workspace", ws.Name)
				}
				if ws.Description != "" {
					t.Errorf("expected empty description, got %q", ws.Description)
				}
			},
		},
		{
			name: "given an empty body when SaveWorkspaceHandler then return 500",
			body: "",
			setupProvider: func(t *testing.T) models.Provider {
				return newInMemoryWorkspaceProvider(t)
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "given malformed JSON when SaveWorkspaceHandler then return 500",
			body: "{not-valid-json}",
			setupProvider: func(t *testing.T) models.Provider {
				return newInMemoryWorkspaceProvider(t)
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			// SaveWorkspaceHandler maps provider errors to HTTP 404 Not Found.
			// The test validates this existing handler behaviour.
			name: "given a provider failure when SaveWorkspaceHandler then return 404",
			body: `{"name":"test-workspace","organization_id":"00000000-0000-0000-0000-000000000002"}`,
			setupProvider: func(t *testing.T) models.Provider {
				base := newInMemoryWorkspaceProvider(t)
				return &failingWorkspaceProvider{DefaultLocalProvider: base}
			},
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := tt.setupProvider(t)

			h := &Handler{
				config: &models.HandlerConfig{},
				log:    log,
			}

			req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()

			h.SaveWorkspaceHandler(w, req, nil, nil, provider)

			resp := w.Result()
			t.Cleanup(func() {
				if err := resp.Body.Close(); err != nil {
					t.Errorf("failed to close response body: %v", err)
				}
			})

			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("expected status %d, got %d (body: %s)", tt.expectedStatus, resp.StatusCode, w.Body.String())
			}

			if tt.validateBody != nil {
				tt.validateBody(t, w.Body.String())
			}
		})
	}
}
