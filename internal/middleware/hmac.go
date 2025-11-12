package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

	v1 "github.com/go-nunu/nunu-layout-advanced/api/v1"
	"github.com/go-nunu/nunu-layout-advanced/pkg/log"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// HMACSignatureMiddleware HMAC-SHA512签名验证中间件（简化版本）
// 通过此中间件验证后，不再需要JWT认证
func HMACSignatureMiddleware(logger *log.Logger, conf *viper.Viper) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取必需的请求头
		timestamp := c.Request.Header.Get("Timestamp")
		nonce := c.Request.Header.Get("Nonce")
		signature := c.Request.Header.Get("Signature")
		appId := c.Request.Header.Get("AppId")

		// 记录调试信息
		logger.WithContext(c).Debug("HMAC signature verification",
			zap.String("timestamp", timestamp),
			zap.String("nonce", nonce),
			zap.String("signature", signature),
			zap.String("appId", appId),
		)

		// 验证必需的请求头
		requiredHeaders := map[string]string{
			"Timestamp": timestamp,
			"Nonce":     nonce,
			"Signature": signature,
			"AppId":     appId,
		}

		for headerName, headerValue := range requiredHeaders {
			if headerValue == "" {
				logger.WithContext(c).Warn("Missing required header", zap.String("header", headerName))
				v1.HandleError(c, http.StatusBadRequest, v1.ErrMissingSignatureHeaders, nil)
				c.Abort()
				return
			}
		}

		// 验证 AppId 是否匹配配置
		configuredAppId := conf.GetString("security.hmac_signature.app_id")
		if configuredAppId == "" {
			logger.WithContext(c).Error("HMAC app_id not configured")
			v1.HandleError(c, http.StatusInternalServerError, v1.ErrSignVerifyFailed, nil)
			c.Abort()
			return
		}

		if appId != configuredAppId {
			logger.WithContext(c).Warn("AppId verification failed",
				zap.String("expected", configuredAppId),
				zap.String("received", appId),
			)
			v1.HandleError(c, http.StatusBadRequest, v1.ErrSignVerifyFailed, nil)
			c.Abort()
			return
		}

		// 读取请求体
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			logger.WithContext(c).Error("Failed to read request body", zap.Error(err))
			v1.HandleError(c, http.StatusBadRequest, v1.ErrSignVerifyFailed, nil)
			c.Abort()
			return
		}

		// 重置请求体以供后续处理器使用
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		// 构建payload
		payload := buildPayload(string(bodyBytes), timestamp, nonce)

		// 对payload进行脱敏处理以避免日志中包含base64图片数据
		sanitizedPayload := log.SanitizeRequestBody([]byte(payload))
		logger.WithContext(c).Debug("Built payload for verification", zap.String("payload", sanitizedPayload))

		// 从配置获取密钥（简化版本）
		secretKey := conf.GetString("security.hmac_signature.secret_key")
		if secretKey == "" {
			logger.WithContext(c).Error("HMAC secret key not configured")
			v1.HandleError(c, http.StatusInternalServerError, v1.ErrSignVerifyFailed, nil)
			c.Abort()
			return
		}

		// 验证签名
		if !verifySignature([]byte(secretKey), []byte(payload), signature) {
			logger.WithContext(c).Warn("Signature verification failed",
				zap.String("expected_payload", log.SanitizeRequestBody([]byte(payload))),
				zap.String("received_signature", signature),
			)
			v1.HandleError(c, http.StatusBadRequest, v1.ErrSignVerifyFailed, nil)
			c.Abort()
			return
		}

		logger.WithContext(c).Info("Signature verification successful")

		// 设置认证标识，表示已通过HMAC签名验证
		c.Set("hmac_authenticated", true)
		c.Set("hmac_app_id", appId)

		c.Next()
	}
}

// buildPayload 构建用于签名的payload字符串
func buildPayload(body, timestamp, nonce string) string {
	return fmt.Sprintf("%s\n%s\n%s\n", timestamp, nonce, body)
}

// computeHMACSignature 计算HMAC-SHA512签名
func computeHMACSignature(secretKey, payload []byte) (string, error) {
	h := hmac.New(sha512.New, secretKey)
	_, err := h.Write(payload)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

// verifySignature 验证签名是否正确
func verifySignature(secretKey, payload []byte, receivedSignature string) bool {
	expectedSignature, err := computeHMACSignature(secretKey, payload)
	if err != nil {
		return false
	}
	return expectedSignature == receivedSignature
}
