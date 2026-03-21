package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jamesdrando/alcatraz/internal/runs"
	rtpkg "github.com/jamesdrando/alcatraz/internal/runtime"
)

const protocolVersion = "2025-06-18"

type Server struct {
	name    string
	version string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type runToolInput struct {
	ConfigPath        string            `json:"config_path,omitempty"`
	BaseRef           string            `json:"base_ref,omitempty"`
	BranchName        string            `json:"branch_name,omitempty"`
	MergeTarget       string            `json:"merge_target,omitempty"`
	ClaimMode         runs.RunClaimMode `json:"claim_mode,omitempty"`
	OwnedPaths        []string          `json:"owned_paths,omitempty"`
	CoordinationPaths []string          `json:"coordination_paths,omitempty"`
	AllowDirty        bool              `json:"allow_dirty,omitempty"`
	ExtraAgentArgs    []string          `json:"extra_agent_args,omitempty"`
}

type getConfigInput struct {
	ConfigPath string `json:"config_path,omitempty"`
}

type diffRunInput struct {
	RunID string `json:"run_id"`
	Stat  bool   `json:"stat,omitempty"`
}

type getRunInput struct {
	RunID string `json:"run_id"`
}

type cleanRunInput struct {
	RunID        string `json:"run_id"`
	DeleteBranch bool   `json:"delete_branch,omitempty"`
}

type cleanAllInput struct {
	DeleteBranch bool `json:"delete_branch,omitempty"`
}

type finishRunInput struct {
	RunID              string                   `json:"run_id"`
	CommitMessage      string                   `json:"commit_message,omitempty"`
	Status             runs.RunCompletionStatus `json:"status,omitempty"`
	Summary            string                   `json:"summary,omitempty"`
	NeedsChanges       []runs.ChangeRequest     `json:"needs_changes,omitempty"`
	Assumptions        []string                 `json:"assumptions,omitempty"`
	SuggestedFollowups []string                 `json:"suggested_followups,omitempty"`
	Merge              bool                     `json:"merge,omitempty"`
	MergeInto          string                   `json:"merge_into,omitempty"`
	Clean              bool                     `json:"clean,omitempty"`
	DeleteBranch       bool                     `json:"delete_branch,omitempty"`
}

type runToolResult struct {
	RunID             string            `json:"run_id"`
	BranchName        string            `json:"branch_name"`
	BaseCommit        string            `json:"base_commit"`
	MergeTarget       string            `json:"merge_target"`
	ClaimMode         runs.RunClaimMode `json:"claim_mode"`
	OwnedPaths        []string          `json:"owned_paths,omitempty"`
	CoordinationPaths []string          `json:"coordination_paths,omitempty"`
	WorktreePath      string            `json:"worktree_path"`
	ComposeProject    string            `json:"compose_project"`
	AuthMode          rtpkg.AuthMode    `json:"auth_mode"`
}

func New(name, version string) *Server {
	return &Server{name: name, version: version}
}

func (s *Server) Serve(r io.Reader, w io.Writer) error {
	reader := bufio.NewReader(r)
	for {
		payload, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req rpcRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			if err := writeMessage(w, rpcResponse{
				JSONRPC: "2.0",
				Error:   &rpcError{Code: -32700, Message: "parse error"},
			}); err != nil {
				return err
			}
			continue
		}

		if req.Method == "notifications/initialized" {
			continue
		}

		resp := s.handleRequest(req)
		if req.ID == nil {
			continue
		}
		if err := writeMessage(w, resp); err != nil {
			return err
		}
	}
}

func (s *Server) handleRequest(req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities": map[string]any{
					"tools": map[string]any{
						"listChanged": false,
					},
				},
				"serverInfo": map[string]any{
					"name":    s.name,
					"version": s.version,
				},
			},
		}
	case "ping":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.tools(),
			},
		}
	case "tools/call":
		result, err := s.handleToolCall(req.Params)
		if err != nil {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: err.Error()},
			}
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	default:
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found"},
		}
	}
}

func (s *Server) handleToolCall(raw json.RawMessage) (map[string]any, error) {
	var params toolCallParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("invalid tool call params")
	}

	switch params.Name {
	case "alcatraz_run":
		var input runToolInput
		if err := decodeToolArgs(params.Arguments, &input); err != nil {
			return nil, err
		}
		rt, err := rtpkg.Open(rtpkg.OpenOptions{ConfigPath: input.ConfigPath})
		if err != nil {
			return toolError(err.Error()), nil
		}
		svc := runs.New(rt)
		meta, err := svc.Create(runs.CreateOptions{
			BaseRef:           input.BaseRef,
			BranchName:        input.BranchName,
			MergeTarget:       input.MergeTarget,
			ClaimMode:         input.ClaimMode,
			OwnedPaths:        input.OwnedPaths,
			CoordinationPaths: input.CoordinationPaths,
			AllowDirty:        input.AllowDirty,
		})
		if err != nil {
			return toolError(err.Error()), nil
		}
		if err := svc.StartPersistent(meta, input.ExtraAgentArgs); err != nil {
			return toolError(fmt.Sprintf("failed to start run %s: %s", meta.ID, err)), nil
		}
		return toolSuccess(runToolResult{
			RunID:             meta.ID,
			BranchName:        meta.BranchName,
			BaseCommit:        meta.BaseCommit,
			MergeTarget:       meta.MergeTarget,
			ClaimMode:         meta.ClaimMode,
			OwnedPaths:        meta.OwnedPaths,
			CoordinationPaths: meta.CoordinationPaths,
			WorktreePath:      meta.WorktreePath,
			ComposeProject:    meta.ComposeProject,
			AuthMode:          meta.AuthMode,
		}), nil
	case "alcatraz_diff_run":
		var input diffRunInput
		if err := decodeToolArgs(params.Arguments, &input); err != nil {
			return nil, err
		}
		rt, err := rtpkg.Open(rtpkg.OpenOptions{})
		if err != nil {
			return toolError(err.Error()), nil
		}
		svc := runs.New(rt)
		diff, err := svc.Diff(input.RunID, input.Stat)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolSuccess(map[string]any{"diff": diff}), nil
	case "alcatraz_list_runs":
		rt, err := rtpkg.Open(rtpkg.OpenOptions{})
		if err != nil {
			return toolError(err.Error()), nil
		}
		svc := runs.New(rt)
		items, err := svc.ListStatuses()
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolSuccess(map[string]any{"runs": items}), nil
	case "alcatraz_get_run":
		var input getRunInput
		if err := decodeToolArgs(params.Arguments, &input); err != nil {
			return nil, err
		}
		rt, err := rtpkg.Open(rtpkg.OpenOptions{})
		if err != nil {
			return toolError(err.Error()), nil
		}
		svc := runs.New(rt)
		item, err := svc.GetStatus(input.RunID)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolSuccess(item), nil
	case "alcatraz_clean_run":
		var input cleanRunInput
		if err := decodeToolArgs(params.Arguments, &input); err != nil {
			return nil, err
		}
		rt, err := rtpkg.Open(rtpkg.OpenOptions{})
		if err != nil {
			return toolError(err.Error()), nil
		}
		svc := runs.New(rt)
		summary, err := svc.CleanRun(input.RunID, input.DeleteBranch)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolSuccess(summary), nil
	case "alcatraz_clean_all":
		var input cleanAllInput
		if err := decodeToolArgs(params.Arguments, &input); err != nil {
			return nil, err
		}
		rt, err := rtpkg.Open(rtpkg.OpenOptions{})
		if err != nil {
			return toolError(err.Error()), nil
		}
		svc := runs.New(rt)
		summary, err := svc.CleanAll(input.DeleteBranch)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolSuccess(summary), nil
	case "alcatraz_get_config":
		var input getConfigInput
		if err := decodeToolArgs(params.Arguments, &input); err != nil {
			return nil, err
		}
		rt, err := rtpkg.Open(rtpkg.OpenOptions{ConfigPath: input.ConfigPath})
		if err != nil {
			return toolError(err.Error()), nil
		}
		svc := runs.New(rt)
		return toolSuccess(svc.EffectiveConfig()), nil
	case "alcatraz_finish_run":
		var input finishRunInput
		if err := decodeToolArgs(params.Arguments, &input); err != nil {
			return nil, err
		}
		rt, err := rtpkg.Open(rtpkg.OpenOptions{})
		if err != nil {
			return toolError(err.Error()), nil
		}
		svc := runs.New(rt)
		result, err := svc.Finish(runs.FinishOptions{
			RunID:         input.RunID,
			CommitMessage: input.CommitMessage,
			Status:        input.Status,
			Summary:       input.Summary,
			NeedsChanges:  input.NeedsChanges,
			Assumptions:   input.Assumptions,
			Followups:     input.SuggestedFollowups,
			Merge:         input.Merge,
			MergeInto:     input.MergeInto,
			Clean:         input.Clean,
			DeleteBranch:  input.DeleteBranch,
		})
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolSuccess(result), nil
	default:
		return toolError("unknown tool: " + params.Name), nil
	}
}

func (s *Server) tools() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "alcatraz_run",
			Description: "Create an isolated Alcatraz run, start its compose project on the host, and optionally execute the configured agent command inside the run container.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"config_path":  map[string]any{"type": "string"},
					"base_ref":     map[string]any{"type": "string"},
					"branch_name":  map[string]any{"type": "string"},
					"merge_target": map[string]any{"type": "string"},
					"claim_mode": map[string]any{
						"type": "string",
						"enum": []string{
							string(runs.RunClaimModeExclusive),
							string(runs.RunClaimModeShared),
						},
					},
					"owned_paths": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"coordination_paths": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"allow_dirty": map[string]any{"type": "boolean"},
					"extra_agent_args": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:        "alcatraz_diff_run",
			Description: "Return the current diff for one run, using the run's recorded base commit.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{"type": "string"},
					"stat":   map[string]any{"type": "boolean"},
				},
				"required":             []string{"run_id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "alcatraz_list_runs",
			Description: "List all known Alcatraz runs and their current status.",
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			Name:        "alcatraz_get_run",
			Description: "Get one Alcatraz run by run ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{"type": "string"},
				},
				"required":             []string{"run_id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "alcatraz_clean_run",
			Description: "Stop and remove one Alcatraz run and optionally delete its branch.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":        map[string]any{"type": "string"},
					"delete_branch": map[string]any{"type": "boolean"},
				},
				"required":             []string{"run_id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "alcatraz_clean_all",
			Description: "Stop and remove all known Alcatraz runs and optionally delete their branches.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"delete_branch": map[string]any{"type": "boolean"},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:        "alcatraz_finish_run",
			Description: "Commit a run, optionally record structured completion state, optionally merge into the run's merge target, and optionally clean it up.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":         map[string]any{"type": "string"},
					"commit_message": map[string]any{"type": "string"},
					"status": map[string]any{
						"type": "string",
						"enum": []string{
							string(runs.RunCompletionStatusReady),
							string(runs.RunCompletionStatusBlocked),
							string(runs.RunCompletionStatusReadyWithAssumptions),
						},
					},
					"summary": map[string]any{"type": "string"},
					"needs_changes": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path":        map[string]any{"type": "string"},
								"description": map[string]any{"type": "string"},
								"blocking":    map[string]any{"type": "boolean"},
							},
							"required":             []string{"description"},
							"additionalProperties": false,
						},
					},
					"assumptions": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"suggested_followups": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"merge":         map[string]any{"type": "boolean"},
					"merge_into":    map[string]any{"type": "string"},
					"clean":         map[string]any{"type": "boolean"},
					"delete_branch": map[string]any{"type": "boolean"},
				},
				"required":             []string{"run_id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "alcatraz_get_config",
			Description: "Return the effective Alcatraz config after default discovery and overrides.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"config_path": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
	}
}

func decodeToolArgs(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("invalid tool arguments")
	}
	return nil
}

func toolSuccess(value any) map[string]any {
	data, _ := json.Marshal(value)
	return map[string]any{
		"content": []textContent{{
			Type: "text",
			Text: string(data),
		}},
		"structuredContent": value,
		"isError":           false,
	}
}

func toolError(message string) map[string]any {
	payload := map[string]string{"error": message}
	data, _ := json.Marshal(payload)
	return map[string]any{
		"content": []textContent{{
			Type: "text",
			Text: string(data),
		}},
		"structuredContent": payload,
		"isError":           true,
	}
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-length:") {
			if _, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength); err != nil {
				if _, err := fmt.Sscanf(line, "content-length: %d", &contentLength); err != nil {
					return nil, fmt.Errorf("invalid content-length header")
				}
			}
		}
	}

	if contentLength <= 0 {
		return nil, fmt.Errorf("missing content-length header")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeMessage(w io.Writer, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
