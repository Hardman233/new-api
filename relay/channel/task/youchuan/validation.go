package youchuan

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

func validateActionRequest(action string, request any) error {
	switch value := request.(type) {
	case *DiffusionRequest:
		if strings.TrimSpace(value.Text) == "" {
			return errors.New("text is required")
		}
	case *VariationRequest:
		if err := validateJobImage(value.JobID, value.ImageNo); err != nil {
			return err
		}
		if value.Type == nil || (*value.Type != 0 && *value.Type != 1) {
			return errors.New("type must be 0 or 1")
		}
	case *UpscaleRequest:
		if err := validateJobImage(value.JobID, value.ImageNo); err != nil {
			return err
		}
		if value.Type == nil || *value.Type < 0 || *value.Type > 3 {
			return errors.New("type must be between 0 and 3")
		}
	case *RerollRequest:
		return requireJobID(value.JobID)
	case *PanRequest:
		if err := validateJobImage(value.JobID, value.ImageNo); err != nil {
			return err
		}
		if value.Direction == nil || *value.Direction < 0 || *value.Direction > 3 {
			return errors.New("direction must be between 0 and 3")
		}
		if value.Scale < 1.1 || value.Scale > 3.0 {
			return errors.New("scale must be between 1.1 and 3.0")
		}
	case *OutpaintRequest:
		if err := validateJobImage(value.JobID, value.ImageNo); err != nil {
			return err
		}
		if value.Scale < 1.1 || value.Scale > 2.0 {
			return errors.New("scale must be between 1.1 and 2.0")
		}
	case *InpaintRequest:
		if err := validateJobImage(value.JobID, value.ImageNo); err != nil {
			return err
		}
		return validateMask(value.Mask)
	case *RemixRequest:
		if err := validateJobImage(value.JobID, value.ImageNo); err != nil {
			return err
		}
		if strings.TrimSpace(value.RemixPrompt) == "" {
			return errors.New("remixPrompt is required")
		}
		if value.Mode != nil && *value.Mode != 0 && *value.Mode != 1 {
			return errors.New("mode must be 0 or 1")
		}
	case *EditRequest:
		if err := validateJobImage(value.JobID, value.ImageNo); err != nil {
			return err
		}
		if strings.TrimSpace(value.RemixPrompt) == "" {
			return errors.New("remixPrompt is required")
		}
		if err := validateCanvas(value.Canvas, value.ImagePos); err != nil {
			return err
		}
		if value.Mask != nil {
			return validateMask(*value.Mask)
		}
	case *UploadPaintRequest:
		if err := validateHTTPURL(value.ImageURL, "imgUrl"); err != nil {
			return err
		}
		if strings.TrimSpace(value.RemixPrompt) == "" {
			return errors.New("remixPrompt is required")
		}
		if err := validateMask(value.Mask); err != nil {
			return err
		}
		return validateCanvas(value.Canvas, value.ImagePos)
	case *RetextureRequest:
		if err := validateHTTPURL(value.ImageURL, "imgUrl"); err != nil {
			return err
		}
		if strings.TrimSpace(value.RemixPrompt) == "" {
			return errors.New("remixPrompt is required")
		}
	case *RemoveBackgroundRequest:
		return validateHTTPURL(value.ImageURL, "imgUrl")
	case *EnhanceRequest:
		return validateJobImage(value.JobID, value.ImageNo)
	case *VideoDiffusionRequest:
		if strings.TrimSpace(value.Prompt) == "" {
			if err := validateJobImage(value.JobID, value.ImageNo); err != nil {
				return errors.New("prompt or jobId with imageNo is required")
			}
		}
		if value.VideoType != nil && *value.VideoType != 0 && *value.VideoType != 1 {
			return errors.New("videoType must be 0 or 1")
		}
	case *ExtendVideoRequest:
		if err := requireJobID(value.JobID); err != nil {
			return err
		}
		if value.VideoNo == nil || *value.VideoNo < 0 {
			return errors.New("videoNo must be zero or greater")
		}
		if strings.TrimSpace(value.Prompt) == "" {
			return errors.New("prompt is required")
		}
	case *VideoUpscaleRequest:
		if err := requireJobID(value.JobID); err != nil {
			return err
		}
		if value.VideoNo == nil || *value.VideoNo < 0 {
			return errors.New("videoNo must be zero or greater")
		}
	default:
		return fmt.Errorf("unsupported Youchuan action %q", action)
	}
	return nil
}

func validateJobImage(jobID string, imageNo *int) error {
	if err := requireJobID(jobID); err != nil {
		return err
	}
	if imageNo == nil || *imageNo < 0 || *imageNo > 3 {
		return errors.New("imageNo must be between 0 and 3")
	}
	return nil
}

func requireJobID(jobID string) error {
	if strings.TrimSpace(jobID) == "" {
		return errors.New("jobId is required")
	}
	return nil
}

func validateMask(mask Mask) error {
	if len(mask.Areas) == 0 && strings.TrimSpace(mask.URL) == "" {
		return errors.New("mask.areas or mask.url is required")
	}
	if mask.URL != "" {
		if err := validateHTTPURL(mask.URL, "mask.url"); err != nil {
			return err
		}
	}
	for i, area := range mask.Areas {
		if area.Width <= 0 || area.Height <= 0 {
			return fmt.Errorf("mask.areas[%d] width and height must be positive", i)
		}
		if len(area.Points) < 6 || len(area.Points)%2 != 0 {
			return fmt.Errorf("mask.areas[%d].points must contain coordinate pairs", i)
		}
	}
	return nil
}

func validateCanvas(canvas Canvas, position ImagePosition) error {
	if canvas.Width <= 0 || canvas.Height <= 0 {
		return errors.New("canvas width and height must be positive")
	}
	if position.Width <= 0 || position.Height <= 0 {
		return errors.New("imgPos width and height must be positive")
	}
	return nil
}

func validateHTTPURL(raw, field string) error {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an HTTP URL", field)
	}
	fetchSetting := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(
		raw,
		fetchSetting.EnableSSRFProtection,
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	); err != nil {
		return fmt.Errorf("%s is not allowed: %v", field, err)
	}
	return nil
}
