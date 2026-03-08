package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AdminHandler 管理面板 API 处理器
type AdminHandler struct {
	db  *gorm.DB
	cfg *AppConfig
}

// NewAdminHandler 创建管理处理器
func NewAdminHandler(db *gorm.DB, cfg *AppConfig) *AdminHandler {
	return &AdminHandler{db: db, cfg: cfg}
}

// HandleLogin POST /admin/login
func (h *AdminHandler) HandleLogin(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	if req.Token != h.cfg.AdminToken {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Token 错误"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "登录成功", "token": h.cfg.AdminToken})
}

// === 账号管理 ===

// HandleListAccounts GET /admin/accounts
func (h *AdminHandler) HandleListAccounts(c *gin.Context) {
	var accounts []Account
	h.db.Order("id ASC").Find(&accounts)
	c.JSON(http.StatusOK, gin.H{"data": accounts})
}

// HandleCreateAccount POST /admin/accounts
func (h *AdminHandler) HandleCreateAccount(c *gin.Context) {
	var req struct {
		Name     string `json:"name"`
		UserID   string `json:"user_id"`
		JWTToken string `json:"jwt_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	if req.UserID == "" || req.JWTToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id 和 jwt_token 为必填项"})
		return
	}

	account := Account{
		Name:     req.Name,
		UserID:   req.UserID,
		JWTToken: req.JWTToken,
		IsActive: true,
		Status:   "normal",
	}
	if result := h.db.Create(&account); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败: " + result.Error.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "创建成功", "data": account})
}

// HandleUpdateAccount PUT /admin/accounts/:id
func (h *AdminHandler) HandleUpdateAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	var account Account
	if result := h.db.First(&account, id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}

	var req struct {
		Name     *string `json:"name"`
		UserID   *string `json:"user_id"`
		JWTToken *string `json:"jwt_token"`
		IsActive *bool   `json:"is_active"`
		Status   *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	if req.Name != nil {
		account.Name = *req.Name
	}
	if req.UserID != nil {
		account.UserID = *req.UserID
	}
	if req.JWTToken != nil {
		account.JWTToken = *req.JWTToken
	}
	if req.IsActive != nil {
		account.IsActive = *req.IsActive
	}
	if req.Status != nil {
		account.Status = *req.Status
	}

	h.db.Save(&account)
	c.JSON(http.StatusOK, gin.H{"message": "更新成功", "data": account})
}

// HandleDeleteAccount DELETE /admin/accounts/:id
func (h *AdminHandler) HandleDeleteAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	if result := h.db.Delete(&Account{}, id); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// === API Key 管理 ===

// HandleListKeys GET /admin/keys
func (h *AdminHandler) HandleListKeys(c *gin.Context) {
	var keys []APIKey
	h.db.Order("id ASC").Find(&keys)
	c.JSON(http.StatusOK, gin.H{"data": keys})
}

// HandleCreateKey POST /admin/keys
func (h *AdminHandler) HandleCreateKey(c *gin.Context) {
	var req struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	if req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key 为必填项"})
		return
	}

	apiKey := APIKey{
		Key:      req.Key,
		Name:     req.Name,
		IsActive: true,
	}
	if result := h.db.Create(&apiKey); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败: " + result.Error.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "创建成功", "data": apiKey})
}

// HandleDeleteKey DELETE /admin/keys/:id
func (h *AdminHandler) HandleDeleteKey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	if result := h.db.Delete(&APIKey{}, id); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// HandleToggleKey PUT /admin/keys/:id/toggle
func (h *AdminHandler) HandleToggleKey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	var key APIKey
	if result := h.db.First(&key, id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Key 不存在"})
		return
	}

	key.IsActive = !key.IsActive
	h.db.Save(&key)

	c.JSON(http.StatusOK, gin.H{"message": "切换成功", "data": key})
}

// === 统计 ===

// HandleStats GET /admin/stats
func (h *AdminHandler) HandleStats(c *gin.Context) {
	var totalAccounts, activeAccounts, errorAccounts int64
	var totalKeys, activeKeys int64

	h.db.Model(&Account{}).Count(&totalAccounts)
	h.db.Model(&Account{}).Where("is_active = ? AND status = ?", true, "normal").Count(&activeAccounts)
	h.db.Model(&Account{}).Where("status IN ?", []string{"error", "rate_limited"}).Count(&errorAccounts)
	h.db.Model(&APIKey{}).Count(&totalKeys)
	h.db.Model(&APIKey{}).Where("is_active = ?", true).Count(&activeKeys)

	c.JSON(http.StatusOK, gin.H{
		"total_accounts":  totalAccounts,
		"active_accounts": activeAccounts,
		"error_accounts":  errorAccounts,
		"total_keys":      totalKeys,
		"active_keys":     activeKeys,
	})
}

// === 批量导入 ===

// BatchImportItem 批量导入的账号条目
type BatchImportItem struct {
	UserID    *string `json:"userId"`
	Username  *string `json:"username"`
	Email     string  `json:"email"`
	Password  string  `json:"password"`
	Token     string  `json:"token"`
	Timestamp string  `json:"timestamp"`
}

// HandleBatchImport POST /admin/accounts/batch — 批量导入账号（JSON 数组）
func (h *AdminHandler) HandleBatchImport(c *gin.Context) {
	var items []BatchImportItem
	if err := c.ShouldBindJSON(&items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON 格式错误: " + err.Error()})
		return
	}

	result := h.batchImportAccounts(items)
	c.JSON(http.StatusOK, result)
}

// HandleFileImport POST /admin/accounts/import — 通过上传 JSON 文件导入账号
func (h *AdminHandler) HandleFileImport(c *gin.Context) {
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传 JSON 文件"})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取文件失败"})
		return
	}

	var items []BatchImportItem
	if err := json.Unmarshal(data, &items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON 解析失败: " + err.Error()})
		return
	}

	result := h.batchImportAccounts(items)
	c.JSON(http.StatusOK, result)
}

// batchImportAccounts 批量导入账号的核心逻辑
func (h *AdminHandler) batchImportAccounts(items []BatchImportItem) gin.H {
	var imported, skipped, failed int
	var errors []string

	for i, item := range items {
		if item.Token == "" {
			failed++
			errors = append(errors, fmt.Sprintf("#%d: 缺少 token", i+1))
			continue
		}

		// 从 JWT 解码 userId
		userID := ""
		if item.UserID != nil && *item.UserID != "" {
			userID = *item.UserID
		} else {
			// 从 JWT Payload 中提取 userId
			extracted, err := extractUserIDFromJWT(item.Token)
			if err != nil {
				failed++
				errors = append(errors, fmt.Sprintf("#%d: JWT 解析失败: %v", i+1, err))
				continue
			}
			userID = extracted
		}

		// 检查是否已存在相同 userID 的账号
		var count int64
		h.db.Model(&Account{}).Where("user_id = ?", userID).Count(&count)
		if count > 0 {
			skipped++
			continue
		}

		// 构建备注名
		name := item.Email
		if name == "" {
			if item.Username != nil && *item.Username != "" {
				name = *item.Username
			}
		}

		account := Account{
			Name:     name,
			UserID:   userID,
			JWTToken: item.Token,
			Email:    item.Email,    // 保存邮箱用于刷新 token
			Password: item.Password, // 保存密码用于刷新 token
			IsActive: true,
			Status:   "normal",
		}
		if result := h.db.Create(&account); result.Error != nil {
			failed++
			errors = append(errors, fmt.Sprintf("#%d: 创建失败: %v", i+1, result.Error))
			continue
		}
		imported++
	}

	return gin.H{
		"message":  fmt.Sprintf("导入完成: 成功 %d, 跳过 %d, 失败 %d", imported, skipped, failed),
		"imported": imported,
		"skipped":  skipped,
		"failed":   failed,
		"errors":   errors,
	}
}

// extractUserIDFromJWT 从 JWT Token 的 Payload 中提取 userId（无需签名验证）
func extractUserIDFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("无效的 JWT 格式")
	}

	// 解码 Payload 部分（第二段）
	payload := parts[1]
	// 补齐 base64 padding
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("base64 解码失败: %w", err)
	}

	var claims struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return "", fmt.Errorf("JSON 解析失败: %w", err)
	}

	if claims.UserID == "" {
		return "", fmt.Errorf("JWT 中缺少 userId 字段")
	}

	return claims.UserID, nil
}

// === 设置管理 ===

// HandleGetSettings GET /admin/settings — 获取当前设置
func (h *AdminHandler) HandleGetSettings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"admin_token":         h.cfg.AdminToken,
		"default_api_key":     h.cfg.DefaultAPIKey,
		"refresh_concurrency": h.cfg.RefreshConcurrency,
		"refresh_retries":     h.cfg.RefreshRetries,
	})
}

// HandleUpdateSettings PUT /admin/settings — 更新设置
func (h *AdminHandler) HandleUpdateSettings(c *gin.Context) {
	var req struct {
		AdminToken         *string `json:"admin_token"`
		DefaultAPIKey      *string `json:"default_api_key"`
		RefreshConcurrency *int    `json:"refresh_concurrency"`
		RefreshRetries     *int    `json:"refresh_retries"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	var messages []string

	// 更新管理 Token
	if req.AdminToken != nil && *req.AdminToken != "" {
		h.db.Where("key = ?", "admin_token").Assign(SystemSetting{Value: *req.AdminToken}).FirstOrCreate(&SystemSetting{Key: "admin_token"})
		h.cfg.AdminToken = *req.AdminToken
		messages = append(messages, "管理 Token 已更新")
	}

	// 更新默认 API Key
	if req.DefaultAPIKey != nil && *req.DefaultAPIKey != "" {
		oldKey := h.cfg.DefaultAPIKey
		newKey := *req.DefaultAPIKey
		// 更新数据库中旧的 Key 记录
		h.db.Model(&APIKey{}).Where("key = ?", oldKey).Update("key", newKey)
		// 将新的默认 API Key 持久化到 system_settings 表
		h.db.Where("key = ?", "default_api_key").Assign(SystemSetting{Value: newKey}).FirstOrCreate(&SystemSetting{Key: "default_api_key"})
		h.cfg.DefaultAPIKey = newKey
		messages = append(messages, "默认 API Key 已更新")
	}

	// 更新刷新并发数
	if req.RefreshConcurrency != nil && *req.RefreshConcurrency > 0 {
		h.db.Where("key = ?", "refresh_concurrency").Assign(SystemSetting{Value: strconv.Itoa(*req.RefreshConcurrency)}).FirstOrCreate(&SystemSetting{Key: "refresh_concurrency"})
		h.cfg.RefreshConcurrency = *req.RefreshConcurrency
		messages = append(messages, "刷新并发数已更新")
	}

	// 更新刷新重试次数
	if req.RefreshRetries != nil && *req.RefreshRetries > 0 {
		h.db.Where("key = ?", "refresh_retries").Assign(SystemSetting{Value: strconv.Itoa(*req.RefreshRetries)}).FirstOrCreate(&SystemSetting{Key: "refresh_retries"})
		h.cfg.RefreshRetries = *req.RefreshRetries
		messages = append(messages, "刷新重试次数已更新")
	}

	if len(messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未提供任何修改"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":             strings.Join(messages, "，"),
		"admin_token":         h.cfg.AdminToken,
		"default_api_key":     h.cfg.DefaultAPIKey,
		"refresh_concurrency": h.cfg.RefreshConcurrency,
		"refresh_retries":     h.cfg.RefreshRetries,
	})
}

// === Token 刷新 ===

// refreshResult 单个账号刷新结果
type refreshResult struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// HandleRefreshTokens POST /admin/accounts/refresh — 一键刷新所有账号的 JWT Token
func (h *AdminHandler) HandleRefreshTokens(c *gin.Context) {
	// 查找所有有 email+password 的账号
	var accounts []Account
	h.db.Where("email != '' AND password != ''").Find(&accounts)
	if len(accounts) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message": "没有可刷新的账号（缺少 email/password）",
			"total":   0,
			"success": 0,
			"failed":  0,
			"results": []refreshResult{},
		})
		return
	}

	concurrency := h.cfg.RefreshConcurrency
	retries := h.cfg.RefreshRetries

	// 并发控制
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []refreshResult
	var successCount, failedCount int

	for _, acc := range accounts {
		wg.Add(1)
		go func(account Account) {
			defer wg.Done()
			sem <- struct{}{} // 获取信号量
			defer func() { <-sem }()

			result := refreshResult{
				ID:    account.ID,
				Name:  account.Name,
				Email: account.Email,
			}

			// 重试逻辑
			var newToken string
			var lastErr error
			for attempt := 0; attempt < retries; attempt++ {
				newToken, lastErr = h.refreshSingleToken(account.Email, account.Password)
				if lastErr == nil {
					break
				}
				time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			}

			mu.Lock()
			defer mu.Unlock()

			if lastErr != nil {
				result.Success = false
				result.Message = lastErr.Error()
				failedCount++
			} else {
				// 更新数据库中的 token
				h.db.Model(&Account{}).Where("id = ?", account.ID).Updates(map[string]interface{}{
					"jwt_token": newToken,
					"status":    "normal",
				})
				result.Success = true
				result.Message = "刷新成功"
				successCount++
			}
			results = append(results, result)
		}(acc)
	}

	wg.Wait()

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("刷新完成: 成功 %d, 失败 %d", successCount, failedCount),
		"total":   len(accounts),
		"success": successCount,
		"failed":  failedCount,
		"results": results,
	})
}

// refreshSingleToken 刷新单个账号的 JWT Token
func (h *AdminHandler) refreshSingleToken(email, password string) (string, error) {
	loginURL := h.cfg.CodeFlickerBaseURL + "/api/auth/email/login"

	payload := map[string]string{"email": email, "password": password}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", loginURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", h.cfg.CodeFlickerBaseURL)
	req.Header.Set("Referer", h.cfg.CodeFlickerBaseURL+"/login")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("登录失败 HTTP %d: %s", resp.StatusCode, string(body))
	}

	// 从响应头获取新 token
	authHeader := resp.Header.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("响应中未找到 Authorization 头")
	}
	newToken := strings.TrimPrefix(authHeader, "Bearer ")
	if newToken == "" || newToken == authHeader {
		return "", fmt.Errorf("无效的 Authorization 头格式")
	}

	return newToken, nil
}
