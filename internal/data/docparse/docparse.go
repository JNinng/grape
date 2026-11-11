// Package docparse 是 biz.FileParser 的数据层实现：把已通过识别的原始
// 文件字节解析为 UTF-8 markdown 产物（表格转 markdown 表格随正文切分）。
// 解析分两档，嗅探 MIME 是唯一路由依据（扩展名不参与）：
//   - Go 内档（零外部设施）：xlsx 用 excelize，docx 用 archive/zip +
//     encoding/xml 手解 OOXML，html 用 x/net/html 走最小转换；
//   - 外部档（pdf / pptx / 图片 OCR）：POST 到 conf.doc_parse.endpoint 的
//     /parse 路径（请求体为原始文件字节，嗅探事实走 X-Grape-Mime /
//     X-Grape-Filename 头），独立进程隔离恶意构造的解析输入；endpoint 为
//     空即「未配置」，外部档按 biz.ErrFileTypeUnsupported 与
//     biz.ErrFileParseUnavailable 的组合错误拒绝（配置缺失而非文件问题）。
//
// 纯文本类型（text/plain、markdown）不经解析器——识别后直接走 biz 内容
// 识别处理，Parse 收到即拒绝（与 biz 路由白名单互为防线）。测试一律以
// httptest 替身服务与测试内构造的容器字节进行，禁真实网络。
package docparse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/google/wire"
)

// ProviderSet 是文件解析设施的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(New)

// 外部解析协议的边界：
//   - defaultTimeout 是 conf 未设置超时的单次调用上限（与 conf.proto 注释
//     同值）；
//   - maxExternalMarkdownBytes 是解析产物的读取上限（20MB 原件的文本
//     产物量级远低于此；超限按解析失败拒绝，防止失控响应撑爆内存）；
//   - maxExternalErrorBodyBytes 是非 200 响应体取样的上限（错误文案进
//     错误信息供排障，不整读响应）。
const (
	defaultTimeout            = 120 * time.Second
	maxExternalMarkdownBytes  = 32 << 20
	maxExternalErrorBodyBytes = 512
)

// Parser 是 biz.FileParser 的两档解析实现。
type Parser struct {
	endpoint string
	client   *http.Client
}

var _ biz.FileParser = (*Parser)(nil)

// New 构造文件解析器。c 可为 nil（conf 未配置 doc_parse 段）：Go 内档
// 类型照常解析，外部档按「未配置」拒绝——解析链路在装配期按有无端点
// 定型，运行期改配置需重启。
func New(c *conf.DocParse) (biz.FileParser, func(), error) {
	endpoint := ""
	timeout := defaultTimeout
	if c != nil {
		endpoint = strings.TrimRight(strings.TrimSpace(c.GetEndpoint()), "/")
		if t := c.GetTimeout().AsDuration(); t > 0 {
			timeout = t
		}
	}
	transport := &http.Transport{}
	client := &http.Client{Timeout: timeout, Transport: transport}
	return &Parser{endpoint: endpoint, client: client}, func() { transport.CloseIdleConnections() }, nil
}

// Parse 实现 biz.FileParser：按嗅探 MIME 分派内档解析器或外部解析服务。
func (p *Parser) Parse(ctx context.Context, in *biz.FileParseInput) (string, error) {
	if in == nil || len(in.Body) == 0 {
		return "", errors.New("docparse: parse input body is required")
	}
	switch normalizeMIME(in.MIME) {
	case mimeHTML:
		return htmlToMarkdown(in.Body)
	case mimeXLSX:
		return xlsxToMarkdown(in.Body)
	case mimeDOCX:
		return docxToMarkdown(in.Body)
	case mimePDF, mimePPTX, mimeJPEG, mimePNG:
		return p.parseExternal(ctx, in)
	case mimePlainText, mimeMarkdown:
		// 纯文本不经解析器（识别后直接内容识别处理），收到即拒绝。
		return "", fmt.Errorf("%w: plain text files are ingested without parsing", biz.ErrFileTypeUnsupported)
	default:
		return "", fmt.Errorf("%w: mime %q has no parser", biz.ErrFileTypeUnsupported, in.MIME)
	}
}

// parseExternal 调用外部解析服务：POST {endpoint}/parse，请求体为原始
// 文件字节，嗅探事实走自定义头。成功 200 且响应体即 markdown 文本；
// 未配置端点按组合错误拒绝（biz 入口层据此区分文案），其余状态码与
// 超限响应按解析失败处理（原样上抛，由摄取编排折叠为失败留痕）。
func (p *Parser) parseExternal(ctx context.Context, in *biz.FileParseInput) (string, error) {
	if p.endpoint == "" {
		return "", fmt.Errorf("%w: %v", biz.ErrFileTypeUnsupported, biz.ErrFileParseUnavailable)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/parse", bytes.NewReader(in.Body))
	if err != nil {
		return "", fmt.Errorf("docparse: build external parse request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Grape-Mime", normalizeMIME(in.MIME))
	req.Header.Set("X-Grape-Filename", in.FileName)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("docparse: call external parse service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		head, _ := io.ReadAll(io.LimitReader(resp.Body, maxExternalErrorBodyBytes))
		return "", fmt.Errorf("docparse: external parse service status %d: %s", resp.StatusCode, strings.TrimSpace(string(head)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxExternalMarkdownBytes+1))
	if err != nil {
		return "", fmt.Errorf("docparse: read external parse response: %w", err)
	}
	if len(body) > maxExternalMarkdownBytes {
		return "", errors.New("docparse: external parse response exceeds size limit")
	}
	return string(body), nil
}

// mime 词表（与 biz.fileparser 的路由词表一致的嗅探产物形态）。
const (
	mimePlainText = "text/plain"
	mimeMarkdown  = "text/markdown"
	mimeHTML      = "text/html"
	mimeXLSX      = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	mimeDOCX      = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimePPTX      = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	mimePDF       = "application/pdf"
	mimePNG       = "image/png"
	mimeJPEG      = "image/jpeg"
)

// normalizeMIME 截掉 MIME 参数（"; charset=..."）并小写化（防御性归一，
// biz 识别产出的 MIME 已归一）。
func normalizeMIME(mime string) string {
	if at := strings.IndexByte(mime, ';'); at >= 0 {
		mime = mime[:at]
	}
	return strings.ToLower(strings.TrimSpace(mime))
}

// mdCell 把单元格文本收敛为 markdown 表格单元格：竖线转义、换行折叠为
// 空格（markdown 表格单元格不能跨行）。
func mdCell(text string) string {
	text = strings.ReplaceAll(text, "|", "\\|")
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	return strings.TrimSpace(text)
}

// mdHeadingText 收敛标题文本：换行折叠为空格（标题单行产出）。
func mdHeadingText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	return strings.TrimSpace(text)
}
