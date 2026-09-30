package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"gorm.io/gorm"
)

type RemoteMirrorUserParams struct {
	SourceRegion string
	SourceUserID int
	Username     string
	DisplayName  string
	Email        string
	Role         int
	Status       int
	Group        string
	InitialQuota int
	UpdateRole   bool
	UpdateGroup  bool
}

func UpsertRemoteMirrorUser(params RemoteMirrorUserParams) (*User, error) {
	params.SourceRegion = strings.TrimSpace(params.SourceRegion)
	params.Username = strings.TrimSpace(params.Username)
	params.DisplayName = strings.TrimSpace(params.DisplayName)
	params.Email = strings.TrimSpace(params.Email)
	params.Group = strings.TrimSpace(params.Group)
	if params.SourceRegion == "" || params.SourceUserID <= 0 || params.Username == "" {
		return nil, errors.New("remote mirror user source and username are required")
	}
	if params.Role <= 0 || !common.IsValidateRole(params.Role) {
		params.Role = common.RoleCommonUser
	}
	if params.Status <= 0 {
		params.Status = common.UserStatusEnabled
	}
	if params.Group == "" {
		params.Group = "default"
	}

	var user User
	err := DB.Where("source_region = ? AND source_user_id = ?", params.SourceRegion, params.SourceUserID).First(&user).Error
	if err == nil {
		updates := map[string]interface{}{
			"username":       params.Username,
			"display_name":   params.DisplayName,
			"email":          params.Email,
			"status":         params.Status,
			"source_region":  params.SourceRegion,
			"source_user_id": params.SourceUserID,
		}
		if params.UpdateRole {
			updates["role"] = params.Role
		}
		if params.UpdateGroup {
			updates["group"] = params.Group
		}
		if err = DB.Model(&user).Updates(updates).Error; err != nil {
			return nil, err
		}
		if err = DB.First(&user, user.Id).Error; err != nil {
			return nil, err
		}
		return &user, updateUserCache(user)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var existing User
	if err = DB.Where("username = ?", params.Username).First(&existing).Error; err == nil {
		return nil, fmt.Errorf("local username %s already exists", params.Username)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	password, err := common.Password2Hash(common.GetRandomString(16))
	if err != nil {
		return nil, err
	}
	user = User{
		Username:     params.Username,
		Password:     password,
		DisplayName:  params.DisplayName,
		Email:        params.Email,
		Role:         params.Role,
		Status:       params.Status,
		Group:        params.Group,
		Quota:        params.InitialQuota,
		AffCode:      common.GetRandomString(8),
		SourceRegion: params.SourceRegion,
		SourceUserID: params.SourceUserID,
	}
	user.SetSetting(dto.UserSetting{})
	if err = DB.Create(&user).Error; err != nil {
		return nil, err
	}

	defaultSidebarConfig := generateDefaultSidebarConfigForRole(user.Role)
	if defaultSidebarConfig != "" {
		currentSetting := user.GetSetting()
		currentSetting.SidebarModules = defaultSidebarConfig
		user.SetSetting(currentSetting)
		_ = DB.Model(&user).Update("setting", user.Setting).Error
	}
	return &user, updateUserCache(user)
}
