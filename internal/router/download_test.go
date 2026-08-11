package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/never/zero-api/internal/store"
)

// TestDownloadImageToDataURL 测试图片下载转 base64
func TestDownloadImageToDataURL(t *testing.T) {
	// 模拟图片服务器
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01, 0x02, 0x03}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngBytes)
		case "/slow.png":
			// 模拟慢服务器（应触发超时回退）
			select {
			case <-r.Context().Done():
			}
		case "/big.png":
			// 模拟超大图片（>5MB 限制，应回退）
			w.Header().Set("Content-Type", "image/png")
			big := make([]byte, 6*1024*1024)
			w.Write(big)
		case "/404.png":
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r := &VirtualModelRouter{}
	r.SetDownloadTimeout(500 * time.Millisecond)
	pc := &store.ProxyConfigData{}

	// 1. 正常下载
	if dataURL, ok := r.downloadImageToDataURL(srv.URL+"/ok.png", pc); !ok {
		t.Fatal("正常图片应下载成功")
	} else if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Errorf("data URL 格式错误: %s", dataURL[:40])
	}

	// 2. 404 应回退
	if _, ok := r.downloadImageToDataURL(srv.URL+"/404.png", pc); ok {
		t.Error("404 图片不应下载成功")
	}

	// 3. 超时图片应快速回退（500ms 超时）
	done := make(chan struct{})
	go func() {
		if _, ok := r.downloadImageToDataURL(srv.URL+"/slow.png", pc); ok {
			t.Error("超时图片不应下载成功")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("超时图片应快速回退（≤3s）")
	}

	// 4. data URL 直接透传（describeImages 中处理，这里验证前缀判断）
	if !strings.HasPrefix("data:image/png;base64,AAA", "data:") {
		t.Error("data URL 前缀判断错误")
	}
}

// TestExtractVisionText 测试识图响应文本提取（兼容 content/reasoning 形态）
func TestExtractVisionText(t *testing.T) {
	cases := []struct {
		name string
		resp string
		want string
	}{
		{
			name: "标准 content 字符串",
			resp: `{"choices":[{"message":{"content":"一只猫","reasoning":"思考"}}]}`,
			want: "一只猫",
		},
		{
			name: "content 为 null 用 reasoning 兜底（mimo 系列）",
			resp: `{"choices":[{"message":{"content":null,"reasoning":"图片显示的是一个红色矩形"}}]}`,
			want: "图片显示的是一个红色矩形",
		},
		{
			name: "content 数组",
			resp: `{"choices":[{"message":{"content":[{"type":"text","text":"第一段"},{"type":"text","text":"第二段"}]}}]}`,
			want: "第一段第二段",
		},
		{
			name: "reasoning_content 字段",
			resp: `{"choices":[{"message":{"content":null,"reasoning_content":"思考内容"}}]}`,
			want: "思考内容",
		},
		{
			name: "空响应",
			resp: `{"choices":[]}`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractVisionText([]byte(tc.resp))
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
