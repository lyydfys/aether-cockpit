package openaicompat

// AWS Signature Version 4（用于 Bedrock 的 IAM AccessKey 认证）。
// Bedrock 的 OpenAI 兼容端点同时支持 API Key Bearer 与 SigV4；这里实现后者。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	sigV4Algorithm  = "AWS4-HMAC-SHA256"
	sigV4DateFormat = "20060102T150405Z"
)

func signAWSV4(req *http.Request, body []byte, accessKey, secretKey, sessionToken, region, service string, now time.Time) error {
	if accessKey == "" || secretKey == "" {
		return fmt.Errorf("SigV4 需要 AccessKey 和 SecretKey")
	}
	now = now.UTC()
	amzDate := now.Format(sigV4DateFormat)
	req.Header.Set("X-Amz-Date", amzDate)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}
	payloadHash := sha256Hex(body)
	if body != nil {
		req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	}

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := strings.ReplaceAll(req.URL.Query().Encode(), "+", "%20")

	headerNames := []string{"host", "x-amz-date"}
	for _, name := range []string{"content-type", "x-amz-content-sha256", "x-amz-security-token"} {
		if strings.TrimSpace(req.Header.Get(name)) != "" {
			headerNames = append(headerNames, name)
		}
	}
	sort.Strings(headerNames)
	var canonicalHeaders strings.Builder
	for _, name := range headerNames {
		value := req.Header.Get(name)
		if name == "host" {
			value = req.Host
			if value == "" {
				value = req.URL.Host
			}
		}
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(normalizeSigV4Header(value))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(headerNames, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")
	scopeDate := now.Format("20060102")
	scope := scopeDate + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		sigV4Algorithm, amzDate, scope, sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	key := sigV4SigningKey(secretKey, scopeDate, region, service)
	signature := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		sigV4Algorithm, accessKey, scope, signedHeaders, signature))
	return nil
}

func normalizeSigV4Header(v string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func sigV4SigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

// bedrockRegionFromHost 从 bedrock-runtime.<region>.amazonaws.com 解析区域。
func bedrockRegionFromHost(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) >= 3 && parts[0] == "bedrock-runtime" {
		return parts[1]
	}
	return "us-east-1"
}
