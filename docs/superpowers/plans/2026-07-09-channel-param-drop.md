# Channel Param Drop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a channel-level `param_drop` configuration that removes selected top-level JSON parameters from upstream outbound request bodies.

**Architecture:** Reuse the existing channel request-body mutation stage that currently applies `param_override`. Add a small helper that deletes configured top-level keys after overrides are merged, then record the final upstream payload exactly as sent. Keep OpenAI Chat transformer behavior unchanged by default; NIM/vLLM compatibility is controlled by channel configuration such as `["prompt_cache_key"]`.

**Tech Stack:** Go, Gin, GORM, JSON request-body rewriting, Next.js/TypeScript, next-intl locale JSON.

## Global Constraints

- `param_drop` is channel-level configuration stored next to `param_override`.
- `param_drop` is a JSON array of string field names, for example `["prompt_cache_key","store"]`.
- Only top-level request JSON keys are removed in this change.
- `param_override` remains merge-only; do not change `null` to mean delete.
- Apply order is transformer output -> `param_override` -> `param_drop` -> metrics payload recording.
- If the request body is not a JSON object, invalid JSON, or `param_drop` is empty/invalid, preserve the existing request body and do not fail the relay.
- Before executing code changes, remove the temporary local change that made `chatPromptCacheKey` stop deriving Anthropic cache metadata. The permanent fix is configuration-driven.

---

## File Structure

- Modify `internal/helper/param_override.go`: add `ApplyParamDrop` and keep `ApplyParamOverride` behavior unchanged.
- Add `internal/helper/param_override_test.go`: unit tests for override merge and drop behavior.
- Modify `internal/model/channel.go`: add `ParamDrop` to `Channel` and `ChannelUpdateRequest`.
- Modify `internal/op/channel.go`: persist `ParamDrop` on channel update.
- Modify `internal/relay/relay.go`: apply `ParamDrop` after `ParamOverride`.
- Modify `internal/relay/relay_test.go`: integration test that an upstream server does not receive dropped parameters.
- Modify `web/src/api/endpoints/channel.ts`: add `param_drop` to channel types and create/update requests.
- Modify `web/src/components/modules/channel/Form.tsx`: add form field state and textarea for `param_drop`.
- Modify `web/src/components/modules/channel/Create.tsx`: submit trimmed `param_drop` on create and reset it.
- Modify `web/src/components/modules/channel/CardContent.tsx`: include `param_drop` in edit form diff/patch.
- Modify `web/public/locale/en.json`, `web/public/locale/zh_hans.json`, `web/public/locale/zh_hant.json`: add labels/placeholders.
- Optional follow-up if site projected channels need the same feature: mirror `param_drop` in `internal/op/site_channel.go`, `web/src/api/endpoints/site-channel.ts`, and `web/src/components/modules/site-channel/index.tsx`. Do not include this optional scope in the first implementation unless requested.

---

### Task 1: Add Request Body Param Drop Helper

**Files:**
- Modify: `internal/helper/param_override.go`
- Add: `internal/helper/param_override_test.go`

**Interfaces:**
- Consumes: `*http.Request` with a JSON-object body.
- Produces: `func ApplyParamDrop(request *http.Request, paramDrop *string) error`.

- [ ] **Step 1: Write failing helper tests**

Create `internal/helper/param_override_test.go` with:

```go
package helper

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func requestBody(t *testing.T, req *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return string(body)
}

func TestApplyParamDropRemovesTopLevelKeys(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://upstream.test/v1/chat/completions", strings.NewReader(`{"model":"gpt","prompt_cache_key":"cache","messages":[],"store":true}`))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	drop := `["prompt_cache_key","store"]`

	if err := ApplyParamDrop(req, &drop); err != nil {
		t.Fatalf("ApplyParamDrop returned error: %v", err)
	}

	got := requestBody(t, req)
	if strings.Contains(got, "prompt_cache_key") || strings.Contains(got, "store") {
		t.Fatalf("expected dropped keys to be absent, got %s", got)
	}
	if !strings.Contains(got, `"model":"gpt"`) || !strings.Contains(got, `"messages":[]`) {
		t.Fatalf("expected remaining keys to be preserved, got %s", got)
	}
}

func TestApplyParamDropIgnoresInvalidConfigAndRestoresBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://upstream.test/v1/chat/completions", strings.NewReader(`{"model":"gpt","prompt_cache_key":"cache"}`))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	drop := `{"prompt_cache_key":true}`

	if err := ApplyParamDrop(req, &drop); err != nil {
		t.Fatalf("ApplyParamDrop returned error: %v", err)
	}

	if got := requestBody(t, req); got != `{"model":"gpt","prompt_cache_key":"cache"}` {
		t.Fatalf("expected original body to be restored, got %s", got)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/helper -run TestApplyParamDrop -count=1
```

Expected: FAIL because `ApplyParamDrop` is undefined.

- [ ] **Step 3: Implement `ApplyParamDrop`**

In `internal/helper/param_override.go`, add:

```go
// ApplyParamDrop removes top-level JSON keys from an outbound JSON request body.
// Empty configs, invalid configs, nil bodies, and non-object request bodies are ignored.
func ApplyParamDrop(request *http.Request, paramDrop *string) error {
	if request == nil || request.Body == nil || paramDrop == nil || strings.TrimSpace(*paramDrop) == "" {
		return nil
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body: %w", err)
	}

	restoreBody := func() {
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		restoreBody()
		return nil
	}

	var drops []string
	if err := json.Unmarshal([]byte(*paramDrop), &drops); err != nil {
		restoreBody()
		return nil
	}

	for _, key := range drops {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		delete(bodyMap, key)
	}

	modifiedBody, err := json.Marshal(bodyMap)
	if err != nil {
		return fmt.Errorf("failed to marshal request body with param drop: %w", err)
	}

	request.Body = io.NopCloser(bytes.NewReader(modifiedBody))
	request.ContentLength = int64(len(modifiedBody))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(modifiedBody)), nil
	}
	return nil
}
```

- [ ] **Step 4: Run helper tests and verify pass**

Run:

```bash
go test ./internal/helper -run TestApplyParamDrop -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/helper/param_override.go internal/helper/param_override_test.go
git commit -m "feat: add channel param drop helper"
```

---

### Task 2: Persist `param_drop` on Channels

**Files:**
- Modify: `internal/model/channel.go`
- Modify: `internal/op/channel.go`

**Interfaces:**
- Consumes: JSON field `param_drop` in create/update channel requests.
- Produces: `model.Channel.ParamDrop *string` persisted by GORM AutoMigrate and updates.

- [ ] **Step 1: Add model fields**

In `internal/model/channel.go`, add `ParamDrop` after `ParamOverride` in `Channel`:

```go
	ParamOverride *string               `json:"param_override"`
	ParamDrop     *string               `json:"param_drop"`
	ChannelProxy  *string               `json:"-" gorm:"column:channel_proxy"`
```

Add `ParamDrop` after `ParamOverride` in `ChannelUpdateRequest`:

```go
	ParamOverride *string                `json:"param_override,omitempty"`
	ParamDrop     *string                `json:"param_drop,omitempty"`
	MatchRegex    *string                `json:"match_regex,omitempty"`
```

- [ ] **Step 2: Add channel update persistence**

In `internal/op/channel.go`, next to the existing `ParamOverride` block, add:

```go
	if req.ParamDrop != nil {
		selectFields = append(selectFields, "param_drop")
		updates.ParamDrop = req.ParamDrop
	}
```

- [ ] **Step 3: Run targeted channel package tests**

Run:

```bash
go test ./internal/op ./internal/model -run 'Test.*Channel|Test.*Site' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/model/channel.go internal/op/channel.go
git commit -m "feat: persist channel param drop config"
```

---

### Task 3: Apply `param_drop` in Relay and Restore Chat Default Behavior

**Files:**
- Modify: `internal/relay/relay.go`
- Modify: `internal/relay/relay_test.go`
- Modify: `internal/transformer/outbound/openai/chat.go`
- Modify: `internal/transformer/outbound/openai/chat_test.go`

**Interfaces:**
- Consumes: `model.Channel.ParamDrop`.
- Produces: final upstream request payload without configured dropped fields.

- [ ] **Step 1: Restore OpenAI Chat default cache-key behavior**

If `internal/transformer/outbound/openai/chat.go` currently has:

```go
func chatPromptCacheKey(request *model.InternalLLMRequest) *string {
	if request == nil {
		return nil
	}
	return request.PromptCacheKey
}
```

restore it to:

```go
func chatPromptCacheKey(request *model.InternalLLMRequest) *string {
	if request == nil {
		return nil
	}
	if request.PromptCacheKey != nil {
		return request.PromptCacheKey
	}
	key, _ := derivedAnthropicCacheMetadata(request)
	return key
}
```

In `internal/transformer/outbound/openai/chat_test.go`, restore or add the Chat transformer unit test:

```go
func TestBuildChatCompletionsRequestDerivesAnthropicPromptCacheKey(t *testing.T) {
	first := anthropicCacheRequest("latest question")
	second := anthropicCacheRequest("different latest question")

	firstWire := buildChatCompletionsRequest(first)
	secondWire := buildChatCompletionsRequest(second)
	if firstWire.PromptCacheKey == nil || secondWire.PromptCacheKey == nil {
		t.Fatalf("expected derived chat prompt_cache_key, got %+v and %+v", firstWire.PromptCacheKey, secondWire.PromptCacheKey)
	}
	if *firstWire.PromptCacheKey != *secondWire.PromptCacheKey {
		t.Fatalf("expected latest user changes to keep stable chat cache key, got %q and %q", *firstWire.PromptCacheKey, *secondWire.PromptCacheKey)
	}
}
```

- [ ] **Step 2: Write failing relay integration test**

Add this test near `TestHandlerAppliesChannelParamOverride` in `internal/relay/relay_test.go`:

```go
func TestHandlerAppliesChannelParamDropAfterOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)

	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream request body failed: %v", err)
		}
		capturedBody = append([]byte(nil), body...)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"chat.completion","created":1,"model":"drop-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	override := `{"prompt_cache_key":"cache-from-override","temperature":0.2}`
	drop := `["prompt_cache_key"]`
	channel := &model.Channel{
		Name:          "relay-param-drop",
		Type:          outbound.OutboundTypeOpenAIChat,
		Enabled:       true,
		BaseUrls:      []model.BaseUrl{{URL: server.URL + "/v1"}},
		Model:         "drop-model",
		Keys:          []model.ChannelKey{{Enabled: true, ChannelKey: "drop-key"}},
		ParamOverride: &override,
		ParamDrop:     &drop,
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}

	group := &model.Group{Name: "relay-param-drop-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "drop-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"relay-param-drop-group","messages":[{"role":"user","content":"hello"}],"prompt_cache_key":"cache-from-client"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	Handler(inbound.InboundTypeOpenAIChat, c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected relay handler to succeed, got status %d body %s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("unmarshal upstream request failed: %v", err)
	}
	if _, ok := payload["prompt_cache_key"]; ok {
		t.Fatalf("expected prompt_cache_key to be dropped, got %#v", payload["prompt_cache_key"])
	}
	if payload["temperature"] != 0.2 {
		t.Fatalf("expected temperature override to remain, got %#v", payload["temperature"])
	}
}
```

- [ ] **Step 3: Run relay test and verify failure**

Run:

```bash
go test ./internal/relay -run TestHandlerAppliesChannelParamDropAfterOverride -count=1
```

Expected: FAIL because relay does not apply `ParamDrop` yet.

- [ ] **Step 4: Apply `ParamDrop` after `ParamOverride`**

In `internal/relay/relay.go`, update `applyParamOverride`:

```go
// applyParamOverride merges channel-level JSON request overrides, drops configured
// top-level JSON params, and records the final upstream payload.
func (ra *relayAttempt) applyParamOverride(outboundRequest *http.Request) error {
	if err := helper.ApplyParamOverride(outboundRequest, ra.channel.ParamOverride); err != nil {
		return err
	}
	if err := helper.ApplyParamDrop(outboundRequest, ra.channel.ParamDrop); err != nil {
		return err
	}
	if requestBody, readErr := readOutboundRequestBody(outboundRequest); readErr == nil {
		ra.metrics.SetTransportRequestPayload(requestBody, ra.internalRequest.Model)
	}
	return nil
}
```

- [ ] **Step 5: Run relay and OpenAI transformer tests**

Run:

```bash
go test ./internal/relay -run 'TestHandlerAppliesChannelParam(Override|DropAfterOverride)' -count=1
go test ./internal/transformer/outbound/openai -run 'TestBuildChatCompletionsRequest(ForwardsPromptCacheKey|DerivesAnthropicPromptCacheKey)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/relay/relay.go internal/relay/relay_test.go internal/transformer/outbound/openai/chat.go internal/transformer/outbound/openai/chat_test.go
git commit -m "feat: apply channel param drop before relay"
```

---

### Task 4: Add Channel UI and API Types

**Files:**
- Modify: `web/src/api/endpoints/channel.ts`
- Modify: `web/src/components/modules/channel/Form.tsx`
- Modify: `web/src/components/modules/channel/Create.tsx`
- Modify: `web/src/components/modules/channel/CardContent.tsx`
- Modify: `web/public/locale/en.json`
- Modify: `web/public/locale/zh_hans.json`
- Modify: `web/public/locale/zh_hant.json`

**Interfaces:**
- Consumes: user-entered JSON array string in channel advanced settings.
- Produces: `param_drop` in create/update channel API payloads.

- [ ] **Step 1: Add TypeScript API fields**

In `web/src/api/endpoints/channel.ts`, add `param_drop?: string | null` next to `param_override?: string | null` in `Channel`, `CreateChannelRequest`, and `UpdateChannelRequest`:

```ts
    param_override?: string | null;
    param_drop?: string | null;
```

- [ ] **Step 2: Add form data field**

In `web/src/components/modules/channel/Form.tsx`, update `ChannelFormData`:

```ts
    param_override: string;
    param_drop: string;
```

Add a textarea below the parameter override textarea:

```tsx
                        <div className="space-y-2">
                            <label htmlFor={`${idPrefix}-param-drop`} className="text-sm font-medium text-card-foreground">
                                {t('paramDrop')}
                            </label>
                            <textarea
                                id={`${idPrefix}-param-drop`}
                                value={formData.param_drop}
                                onChange={(e) => onFormDataChange({ ...formData, param_drop: e.target.value })}
                                placeholder={t('paramDropPlaceholder')}
                                className="min-h-20 w-full rounded-xl border border-border bg-background px-3 py-2 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                            />
                        </div>
```

- [ ] **Step 3: Submit on create**

In `web/src/components/modules/channel/Create.tsx`, compute:

```ts
        const paramDrop = formData.param_drop.trim();
```

Include it in the create payload:

```ts
                param_override: paramOverride,
                param_drop: paramDrop,
```

Reset it on success:

```ts
                        param_override: '',
                        param_drop: '',
```

- [ ] **Step 4: Submit on update when changed**

In `web/src/components/modules/channel/CardContent.tsx`, add:

```ts
        const nextParamDrop = formData.param_drop.trim();
        const curParamDrop = channel.param_drop ?? '';
        if (nextParamDrop !== curParamDrop) {
            // Empty string means "clear" for patch semantics; backend maps it to NULL.
            req.param_drop = nextParamDrop;
        }
```

Ensure the edit form initialization includes:

```ts
        param_drop: channel.param_drop ?? '',
```

- [ ] **Step 5: Add locale strings**

In `web/public/locale/en.json`, add near channel `paramOverride` strings:

```json
"paramDrop": "Drop parameters JSON",
"paramDropPlaceholder": "Leave empty to clear; use a JSON string array, for example: [\"prompt_cache_key\",\"store\"]"
```

In `web/public/locale/zh_hans.json`, add:

```json
"paramDrop": "丢弃参数 JSON",
"paramDropPlaceholder": "留空表示清除；填写 JSON 字符串数组，例如：[\"prompt_cache_key\",\"store\"]"
```

In `web/public/locale/zh_hant.json`, add:

```json
"paramDrop": "丟棄參數 JSON",
"paramDropPlaceholder": "留空表示清除；填寫 JSON 字串陣列，例如：[\"prompt_cache_key\",\"store\"]"
```

- [ ] **Step 6: Run frontend checks**

Run:

```bash
cd web && pnpm lint
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/api/endpoints/channel.ts web/src/components/modules/channel/Form.tsx web/src/components/modules/channel/Create.tsx web/src/components/modules/channel/CardContent.tsx web/public/locale/en.json web/public/locale/zh_hans.json web/public/locale/zh_hant.json
git commit -m "feat: expose channel param drop setting"
```

---

### Task 5: Final Verification

**Files:**
- No new files.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: verified implementation.

- [ ] **Step 1: Run focused backend tests**

Run:

```bash
go test ./internal/helper ./internal/relay ./internal/transformer/outbound/openai -run 'TestApplyParamDrop|TestHandlerAppliesChannelParam(Override|DropAfterOverride)|TestBuildChatCompletionsRequest(ForwardsPromptCacheKey|DerivesAnthropicPromptCacheKey)' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader backend tests for touched packages**

Run:

```bash
go test ./internal/helper ./internal/model ./internal/op ./internal/relay ./internal/transformer/outbound/openai -count=1
```

Expected: PASS.

- [ ] **Step 3: Run frontend lint**

Run:

```bash
cd web && pnpm lint
```

Expected: PASS.

- [ ] **Step 4: Inspect final diff**

Run:

```bash
git diff --check
git status --short
```

Expected: `git diff --check` prints nothing. `git status --short` shows only intentional files if commits were skipped, or a clean tree if each task was committed.

- [ ] **Step 5: Commit verification-only cleanup if needed**

If any formatting or lint-only cleanup was required:

```bash
git add .
git commit -m "chore: clean up param drop implementation"
```

If no cleanup was required, do not create an empty commit.

---

## Self-Review

- Spec coverage: The plan adds `param_drop`, keeps `param_override` merge-only, applies drop after override, records final payload, and preserves OpenAI Chat default behavior unless the channel config drops fields.
- Placeholder scan: No `TBD`, `TODO`, or unspecified implementation steps remain.
- Type consistency: The backend field name is `ParamDrop`; JSON/API field name is `param_drop`; helper function name is `ApplyParamDrop`; UI form key is `param_drop`.
