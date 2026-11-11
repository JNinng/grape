package biz

import (
	"context"
	"errors"
	"path"
	"strings"
)

// 文件摄取（kind=file）的错误语义：
//   - ErrFileTypeUnsupported：文件类型不被摄取支持——扩展名 / 嗅探 MIME 不在
//     白名单，或外部档类型而外部解析服务未配置（两者同为「该文件现在无法
//     摄取」，入口层映射 FILE_TYPE_UNSUPPORTED，文案区分具体原因）；
//   - ErrFileContentUnreadable：内容识别判定为乱码（转码后不可读字符占比
//     超阈值），是乱码文本 / OCR 失败 / 加密 PDF 的兜底信号；
//   - ErrFileContentTooShort：解析与清洗后正文低于最短阈值（空文档或解析
//     产物退化为噪声）；
//   - 其余解析故障以普通错误上抛（端点故障、容器损坏等），由调用方折入
//     摄取失败语义（目录行保留、可重解析）。
var (
	ErrFileTypeUnsupported   = errors.New("biz: file type unsupported")
	ErrFileContentUnreadable = errors.New("biz: file content unreadable")
	ErrFileContentTooShort   = errors.New("biz: file content too short")
)

// FileParseInput 是解析器的一次输入：MIME 是文件识别（identifyFile）产出
// 的嗅探结果，即解析路由依据；FileName 仅用于日志与外部服务的附带事实，
// 解析不信任其扩展名。Body 是原始文件字节（已通过识别与限流）。
type FileParseInput struct {
	MIME     string
	FileName string
	Body     []byte
}

// FileParser 是文件解析器的框架无关契约：把已通过识别的原始文件字节解析
// 为 UTF-8 markdown 产物（表格转 markdown 表格随正文切分）。实现由 data
// 层能力目录提供（Go 内档 xlsx / docx / html 零外部设施；外部档 pdf /
// pptx / 图片 OCR 走独立进程 + HTTP 的解析服务，端点来自 conf.doc_parse）。
//
// 纯文本类型（text/plain、markdown）不经解析器：识别后直接走内容识别
// 处理入库，Parse 不应收到这些 MIME（收到按 ErrFileTypeUnsupported 拒绝，
// 与路由白名单互为防线）。
type FileParser interface {
	// Parse 解析文件字节为 markdown。MIME 不在任何解析档内返回
	// ErrFileTypeUnsupported；外部档在解析服务未配置时返回
	// fmt.Errorf("%w: %v", ErrFileTypeUnsupported, ErrFileParseUnavailable)
	// 形态的组合错误（按配置缺失而非文件问题拒绝，文案可区分），其余故障
	// 原样上抛。
	Parse(ctx context.Context, in *FileParseInput) (string, error)
}

// ErrFileParseUnavailable 标记「外部档类型但外部解析服务未配置」：入口层
// 据此把拒绝文案与普通不支持类型区分开（提示配置而非更换文件）。
var ErrFileParseUnavailable = errors.New("biz: external document parse service not configured")

// 文件摄取的 MIME 路由档位。嗅探出的 MIME 是唯一路由依据；扩展名只参与
// 快速拒绝与 text/plain 的 markdown 判定（嗅探区分不了 markdown 与普通
// 文本）。
const (
	// FileRouteDirect：免解析直接入库（经内容识别处理统一转码 / 清洗）。
	FileRouteDirect = "direct"
	// FileRouteNative：Go 内档解析（xlsx / docx / html，零外部设施）。
	FileRouteNative = "native"
	// FileRouteExternal：外部解析服务（pdf / pptx / 图片 OCR，独立进程）。
	FileRouteExternal = "external"
	// FileRouteUnsupported：不在白名单，拒绝。
	FileRouteUnsupported = "unsupported"
)

// 内档与外部档的 MIME 词表（小写，无参数；嗅探结果按分号截断后匹配）。
const (
	mimePlainText   = "text/plain"
	mimeMarkdown    = "text/markdown"
	mimeHTML        = "text/html"
	mimeXLSX        = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	mimeDOCX        = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimePPTX        = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	mimePDF         = "application/pdf"
	mimePNG         = "image/png"
	mimeJPEG        = "image/jpeg"
	mimeOctetStream = "application/octet-stream"
)

// extensionMIME 是扩展名白名单：快速拒绝明显不在摄取范围的文件，避免为
// 每个拒绝对象做内容嗅探。值为该扩展名的期望嗅探 MIME（多值任一命中即
// 通过）；期望值只用于「扩展名与内容不一致记风险日志」的比对，路由一律
// 以嗅探结果为准。
var extensionMIME = map[string][]string{
	".txt":      {mimePlainText},
	".text":     {mimePlainText},
	".log":      {mimePlainText},
	".md":       {mimePlainText, mimeMarkdown},
	".markdown": {mimePlainText, mimeMarkdown},
	".html":     {mimeHTML, mimePlainText},
	".htm":      {mimeHTML, mimePlainText},
	".xlsx":     {mimeXLSX},
	".docx":     {mimeDOCX},
	".pdf":      {mimePDF},
	".pptx":     {mimePPTX},
	".jpg":      {mimeJPEG},
	".jpeg":     {mimeJPEG},
	".png":      {mimePNG},
}

// fileMIMECategory 把嗅探 MIME 归入路由档位：纯文本族直接入库；OOXML 文档
// 与 HTML 走 Go 内档；pdf / pptx / 图片走外部档；其余（含 zip 容器伪装、
// 二进制与未识别流）拒绝。嗅探为 octet-stream 且扩展名指向 OOXML 时按
// 该扩展名的档位放行——gabriel-vasile/mimetype 对部分手工构造的 OOXML
// 容器可能回落 octet-stream，扩展名白名单已通过时信任容器预检（zip
// 炸弹预检仍照常执行）。
func fileMIMECategory(mime, ext string) string {
	switch normalizeMIME(mime) {
	case mimePlainText, mimeMarkdown:
		return FileRouteDirect
	case mimeHTML:
		return FileRouteNative
	case mimeXLSX, mimeDOCX:
		return FileRouteNative
	case mimePDF, mimePPTX, mimeJPEG, mimePNG:
		return FileRouteExternal
	case mimeOctetStream:
		switch ext {
		case ".xlsx":
			return FileRouteNative
		case ".docx":
			return FileRouteNative
		case ".pptx":
			return FileRouteExternal
		case ".pdf", ".jpg", ".jpeg", ".png":
			return FileRouteExternal
		}
	}
	return FileRouteUnsupported
}

// normalizeMIME 截掉 MIME 参数（"; charset=..."）并小写化。
func normalizeMIME(mime string) string {
	if at := strings.IndexByte(mime, ';'); at >= 0 {
		mime = mime[:at]
	}
	return strings.ToLower(strings.TrimSpace(mime))
}

// fileExtension 取文件名的小写扩展名（含点）；无扩展名返回空串。
func fileExtension(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ext == "." {
		return ""
	}
	return ext
}

// fileRouteForDirectType 决定纯文本档位的摄取正文形态：markdown 扩展名的
// text/plain 走标题切分（与 markdown 条目同口径），其余按普通文本。
// 嗅探区分不了 markdown 与普通文本（二者都是无 magic 的文本字节），
// 扩展名是唯一可用的判定事实。
func fileRouteForDirectType(ext string) string {
	switch ext {
	case ".md", ".markdown":
		return DocumentKindMarkdown
	default:
		return DocumentKindText
	}
}
