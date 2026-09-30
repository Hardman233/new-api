package youchuan

import "encoding/json"

const DefaultBaseURL = "https://ali.youchuan.cn"

const (
	ModelDiffusion        = "youchuan-diffusion"
	ModelVariation        = "youchuan-variation"
	ModelUpscale          = "youchuan-upscale"
	ModelReroll           = "youchuan-reroll"
	ModelPan              = "youchuan-pan"
	ModelOutpaint         = "youchuan-outpaint"
	ModelInpaint          = "youchuan-inpaint"
	ModelRemix            = "youchuan-remix"
	ModelEdit             = "youchuan-edit"
	ModelUploadPaint      = "youchuan-upload-paint"
	ModelRetexture        = "youchuan-retexture"
	ModelRemoveBackground = "youchuan-remove-background"
	ModelEnhance          = "youchuan-enhance"
	ModelVideoDiffusion   = "youchuan-video-diffusion"
	ModelExtendVideo      = "youchuan-extend-video"
	ModelVideoUpscale     = "youchuan-video-upscale"
)

var ModelList = []string{
	ModelDiffusion,
	ModelVariation,
	ModelUpscale,
	ModelReroll,
	ModelPan,
	ModelOutpaint,
	ModelInpaint,
	ModelRemix,
	ModelEdit,
	ModelUploadPaint,
	ModelRetexture,
	ModelRemoveBackground,
	ModelEnhance,
	ModelVideoDiffusion,
	ModelExtendVideo,
	ModelVideoUpscale,
}

var actionModels = map[string]string{
	"diffusion":         ModelDiffusion,
	"variation":         ModelVariation,
	"upscale":           ModelUpscale,
	"reroll":            ModelReroll,
	"pan":               ModelPan,
	"outpaint":          ModelOutpaint,
	"inpaint":           ModelInpaint,
	"remix":             ModelRemix,
	"edit":              ModelEdit,
	"upload-paint":      ModelUploadPaint,
	"retexture":         ModelRetexture,
	"remove-background": ModelRemoveBackground,
	"enhance":           ModelEnhance,
	"video-diffusion":   ModelVideoDiffusion,
	"extend-video":      ModelExtendVideo,
	"video-upscale":     ModelVideoUpscale,
}

func ModelForAction(action string) (string, bool) {
	model, ok := actionModels[action]
	return model, ok
}

type Mask struct {
	Areas []MaskArea `json:"areas,omitempty"`
	URL   string     `json:"url,omitempty"`
}

type MaskArea struct {
	Width  int   `json:"width"`
	Height int   `json:"height"`
	Points []int `json:"points"`
}

type Canvas struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type ImagePosition struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	X      int `json:"x"`
	Y      int `json:"y"`
}

type DiffusionRequest struct {
	Text     string `json:"text"`
	Callback string `json:"callback,omitempty"`
}

type VariationRequest struct {
	JobID       string `json:"jobId"`
	ImageNo     *int   `json:"imageNo"`
	Type        *int   `json:"type"`
	Callback    string `json:"callback,omitempty"`
	RemixPrompt string `json:"remixPrompt,omitempty"`
}

type UpscaleRequest struct {
	JobID    string `json:"jobId"`
	ImageNo  *int   `json:"imageNo"`
	Type     *int   `json:"type"`
	Callback string `json:"callback,omitempty"`
}

type RerollRequest struct {
	JobID    string `json:"jobId"`
	Callback string `json:"callback,omitempty"`
}

type PanRequest struct {
	JobID       string  `json:"jobId"`
	ImageNo     *int    `json:"imageNo"`
	Direction   *int    `json:"direction"`
	Scale       float64 `json:"scale"`
	RemixPrompt string  `json:"remixPrompt,omitempty"`
	Callback    string  `json:"callback,omitempty"`
}

type OutpaintRequest struct {
	JobID       string  `json:"jobId"`
	ImageNo     *int    `json:"imageNo"`
	Scale       float64 `json:"scale"`
	RemixPrompt string  `json:"remixPrompt,omitempty"`
	Callback    string  `json:"callback,omitempty"`
}

type InpaintRequest struct {
	JobID       string `json:"jobId"`
	ImageNo     *int   `json:"imageNo"`
	Mask        Mask   `json:"mask"`
	RemixPrompt string `json:"remixPrompt,omitempty"`
	Callback    string `json:"callback,omitempty"`
}

type RemixRequest struct {
	JobID       string `json:"jobId"`
	ImageNo     *int   `json:"imageNo"`
	RemixPrompt string `json:"remixPrompt"`
	Mode        *int   `json:"mode,omitempty"`
	Callback    string `json:"callback,omitempty"`
}

type EditRequest struct {
	JobID       string        `json:"jobId"`
	ImageNo     *int          `json:"imageNo"`
	Canvas      Canvas        `json:"canvas"`
	ImagePos    ImagePosition `json:"imgPos"`
	RemixPrompt string        `json:"remixPrompt"`
	Mask        *Mask         `json:"mask,omitempty"`
	Callback    string        `json:"callback,omitempty"`
}

type UploadPaintRequest struct {
	ImageURL    string        `json:"imgUrl"`
	Mask        Mask          `json:"mask"`
	Canvas      Canvas        `json:"canvas"`
	ImagePos    ImagePosition `json:"imgPos"`
	RemixPrompt string        `json:"remixPrompt"`
	Callback    string        `json:"callback,omitempty"`
}

type RetextureRequest struct {
	ImageURL    string `json:"imgUrl"`
	RemixPrompt string `json:"remixPrompt"`
	Callback    string `json:"callback,omitempty"`
}

type RemoveBackgroundRequest struct {
	ImageURL string `json:"imgUrl"`
	Callback string `json:"callback,omitempty"`
}

type EnhanceRequest struct {
	JobID    string `json:"jobId"`
	ImageNo  *int   `json:"imageNo"`
	Callback string `json:"callback,omitempty"`
}

type VideoDiffusionRequest struct {
	JobID     string `json:"jobId,omitempty"`
	ImageNo   *int   `json:"imageNo,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	VideoType *int   `json:"videoType,omitempty"`
	Callback  string `json:"callback,omitempty"`
}

type ExtendVideoRequest struct {
	JobID    string `json:"jobId"`
	VideoNo  *int   `json:"videoNo"`
	Prompt   string `json:"prompt"`
	Callback string `json:"callback,omitempty"`
}

type VideoUpscaleRequest struct {
	JobID    string `json:"jobId"`
	VideoNo  *int   `json:"videoNo"`
	Callback string `json:"callback,omitempty"`
}

type CostQuery struct {
	PageNo   int
	PageSize int
	Since    string
	To       string
}

type MoodboardListQuery struct {
	PageNo   int
	PageSize int
}

type CallbackDemoRequest struct {
	JobID string `json:"jobId"`
}

type MoodboardRequest struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Images      []string `json:"images,omitempty"`
}

type Cost struct {
	JobID     string  `json:"jobId"`
	FastCost  float64 `json:"fastCost"`
	RelaxCost float64 `json:"relaxCost"`
	FeeCost   float64 `json:"feeCost"`
	CostAt    string  `json:"costAt"`
}

type TaskResponse struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	URLs    []string `json:"urls"`
	Status  int      `json:"status"`
	Comment string   `json:"comment"`
	Cost    Cost     `json:"cost"`
	Audits  []string `json:"audits"`
	Seed    uint64   `json:"seed"`
}

type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

type CostHistoryResponse struct {
	Costs []Cost `json:"costs"`
}

type Plan struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	MinCost             string `json:"minCost"`
	StartupCharge       string `json:"startupCharge"`
	FastLimit           string `json:"fastLimit"`
	FastConcurrentNum   string `json:"fastConcurrentNum"`
	FastPerCost         string `json:"fastPerCost"`
	FastOutPlanPerCost  string `json:"fastOutPlanPerCost,omitempty"`
	RelaxLimit          string `json:"relaxLimit"`
	RelaxConcurrentNum  string `json:"relaxConcurrentNum"`
	RelaxPerCost        string `json:"relaxPerCost"`
	RelaxOutPlanPerCost string `json:"relaxOutPlanPerCost,omitempty"`
	VideoConcurrentNum  string `json:"videoConcurrentNum"`
	Ability             string `json:"ability"`
}

type MonthlyCost struct {
	TermStart string  `json:"termStart"`
	TermEnd   string  `json:"termEnd"`
	FeeCost   float64 `json:"feeCost"`
	RelaxCost float64 `json:"relaxCost"`
	Plan      Plan    `json:"plan"`
}

type MonthlyCostResponse struct {
	Costs []MonthlyCost `json:"costs"`
}

type TermCost struct {
	StartAt  string  `json:"startAt"`
	EndAt    string  `json:"endAt"`
	PkgFee   float64 `json:"pkgFee"`
	ExtraFee float64 `json:"extraFee"`
}

type PlanRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SubscribeResponse struct {
	CurrentAvail   int64    `json:"currentAvail"`
	Plan           Plan     `json:"plan"`
	ShouldChargeAt string   `json:"shouldChargeAt"`
	NextTermPlan   PlanRef  `json:"nextTermPlan"`
	CurTermCost    TermCost `json:"curTermCost"`
	CurTermPlan    PlanRef  `json:"curTermPlan"`
}

type Moodboard struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Images      []string `json:"images,omitempty"`
	ImageCount  int      `json:"imageCount,omitempty"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt,omitempty"`
}

type MoodboardListResponse struct {
	Data     []Moodboard `json:"data"`
	Total    int         `json:"total"`
	PageNo   int         `json:"pageNo"`
	PageSize int         `json:"pageSize"`
}

type RawResponse = json.RawMessage
