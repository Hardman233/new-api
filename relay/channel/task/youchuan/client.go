package youchuan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL   string
	AppID     string
	SecretKey string
	HTTP      *http.Client
}

type UpstreamError struct {
	StatusCode int
	Response   ErrorResponse
	Body       []byte
}

func (e *UpstreamError) Error() string {
	message := strings.TrimSpace(e.Response.Message)
	if message == "" {
		message = strings.TrimSpace(string(e.Body))
	}
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	if e.Response.Reason != "" {
		return fmt.Sprintf("Youchuan %s: %s", e.Response.Reason, message)
	}
	return fmt.Sprintf("Youchuan HTTP %d: %s", e.StatusCode, message)
}

func NewClient(baseURL string, credential Credential, httpClient *http.Client) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{
		BaseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		AppID:     credential.AppID,
		SecretKey: credential.Secret,
		HTTP:      CloneNoRedirectClient(httpClient),
	}
}

// CloneNoRedirectClient prevents provider credentials from being forwarded to
// a redirect target. The official API does not require redirects.
func CloneNoRedirectClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	if clone.Timeout == 0 {
		clone.Timeout = 60 * time.Second
	}
	clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-youchuan-app", c.AppID)
	req.Header.Set("x-youchuan-secret", c.SecretKey)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		upstreamErr := &UpstreamError{StatusCode: resp.StatusCode, Body: append([]byte(nil), body...)}
		_ = json.Unmarshal(body, &upstreamErr.Response)
		return upstreamErr
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], body...)
		return nil
	}
	if err = json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode Youchuan response: %w", err)
	}
	return nil
}

func (c *Client) postTask(ctx context.Context, path string, in any) (*TaskResponse, error) {
	var out TaskResponse
	err := c.doJSON(ctx, http.MethodPost, path, nil, in, &out)
	return &out, err
}

func (c *Client) Diffusion(ctx context.Context, in DiffusionRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/diffusion", in)
}
func (c *Client) Variation(ctx context.Context, in VariationRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/variation", in)
}
func (c *Client) Upscale(ctx context.Context, in UpscaleRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/upscale", in)
}
func (c *Client) Reroll(ctx context.Context, in RerollRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/reroll", in)
}
func (c *Client) Pan(ctx context.Context, in PanRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/pan", in)
}
func (c *Client) Outpaint(ctx context.Context, in OutpaintRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/outpaint", in)
}
func (c *Client) Inpaint(ctx context.Context, in InpaintRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/inpaint", in)
}
func (c *Client) Remix(ctx context.Context, in RemixRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/remix", in)
}
func (c *Client) Edit(ctx context.Context, in EditRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/edit", in)
}
func (c *Client) UploadPaint(ctx context.Context, in UploadPaintRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/upload-paint", in)
}
func (c *Client) Retexture(ctx context.Context, in RetextureRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/retexture", in)
}
func (c *Client) RemoveBackground(ctx context.Context, in RemoveBackgroundRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/remove-background", in)
}
func (c *Client) Enhance(ctx context.Context, in EnhanceRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/enhance", in)
}
func (c *Client) VideoDiffusion(ctx context.Context, in VideoDiffusionRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/video-diffusion", in)
}
func (c *Client) ExtendVideo(ctx context.Context, in ExtendVideoRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/extend-video", in)
}
func (c *Client) VideoUpscale(ctx context.Context, in VideoUpscaleRequest) (*TaskResponse, error) {
	return c.postTask(ctx, "/v1/tob/video-upscale", in)
}

func (c *Client) Job(ctx context.Context, jobID string) (*TaskResponse, error) {
	var out TaskResponse
	err := c.doJSON(ctx, http.MethodGet, "/v1/tob/job/"+url.PathEscape(jobID), nil, nil, &out)
	return &out, err
}

func (c *Client) Costs(ctx context.Context, in CostQuery) (*CostHistoryResponse, error) {
	query := pageQuery(in.PageNo, in.PageSize)
	if in.Since != "" {
		query.Set("since", in.Since)
	}
	if in.To != "" {
		query.Set("to", in.To)
	}
	var out CostHistoryResponse
	err := c.doJSON(ctx, http.MethodGet, "/v1/tob/costs", query, nil, &out)
	return &out, err
}

func (c *Client) CostMonthly(ctx context.Context, pageNo, pageSize int) (*MonthlyCostResponse, error) {
	var out MonthlyCostResponse
	err := c.doJSON(ctx, http.MethodGet, "/v1/tob/cost-monthly", pageQuery(pageNo, pageSize), nil, &out)
	return &out, err
}

func (c *Client) Subscribe(ctx context.Context) (*SubscribeResponse, error) {
	var out SubscribeResponse
	err := c.doJSON(ctx, http.MethodGet, "/v1/tob/subscribe", nil, nil, &out)
	return &out, err
}

func (c *Client) CallbackDemo(ctx context.Context, in CallbackDemoRequest) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.doJSON(ctx, http.MethodPost, "/v1/tob/callback", nil, in, &out)
	return out, err
}

func (c *Client) CreateMoodboard(ctx context.Context, in MoodboardRequest) (*Moodboard, error) {
	var out Moodboard
	err := c.doJSON(ctx, http.MethodPost, "/v1/tob/moodboard", nil, in, &out)
	return &out, err
}

func (c *Client) UpdateMoodboard(ctx context.Context, id string, in MoodboardRequest) (*Moodboard, error) {
	var out Moodboard
	err := c.doJSON(ctx, http.MethodPut, "/v1/tob/moodboard/"+url.PathEscape(id), nil, in, &out)
	return &out, err
}

func (c *Client) Moodboards(ctx context.Context, in MoodboardListQuery) (*MoodboardListResponse, error) {
	var out MoodboardListResponse
	err := c.doJSON(ctx, http.MethodGet, "/v1/tob/moodboards", pageQuery(in.PageNo, in.PageSize), nil, &out)
	return &out, err
}

func pageQuery(pageNo, pageSize int) url.Values {
	query := url.Values{}
	if pageNo > 0 {
		query.Set("pageNo", strconv.Itoa(pageNo))
	}
	if pageSize > 0 {
		query.Set("pageSize", strconv.Itoa(pageSize))
	}
	return query
}
