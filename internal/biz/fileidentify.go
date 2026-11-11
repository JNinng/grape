package biz

import (
	"archive/zip"
	"bytes"
	"fmt"
	"log/slog"

	"github.com/gabriel-vasile/mimetype"
)

// 文件识别的三层校验（上传入口，不信任客户端声明）：
//  1. 扩展名白名单快速拒绝——明显不在摄取范围的文件不做内容嗅探；
//  2. 内容嗅探（gabriel-vasile/mimetype，纯 Go 零 cgo）——magic bytes 定
//     真实类型，路由与落库 MIME 一律以嗅探结果为准；扩展名与嗅探不一致
//     记风险日志（file_name 保留原名，mime_type 存嗅探事实）；
//  3. 完整性限流——单文件 ≤ 20MB（与 oss_object 设施上限对齐）；OOXML 是
//     zip 容器，解析前按 entry 数与解压总量上限预检防 zip 炸弹。
//
// 执行序上，字节量上限先于嗅探（不为注定拒绝的超限文件做检测），其余
// 按层推进。安全边界：magic bytes 只防改扩展名的伪装，防不了针对解析器
// 的构造恶意文件；恶意内容的根治是解析器独立进程隔离（外部档），Go 内档
// 的缓解是容器预检与解析实现的只读、有界读取。
const (
	// maxFileBytes 是单文件字节上限，与内嵌 OSS 的 PutObject 限流同值：
	// 超限在识别期拒绝，不让字节进对象存储。
	maxFileBytes = 20 << 20
	// maxArchiveEntries 是 zip 容器的 entry 数上限（正常 OOXML 十量级，
	// 上限留出一个数量级以上的余量）。
	maxArchiveEntries = 512
	// maxArchiveUncompressed 是 zip 容器声明的解压总量上限（20MB 压缩包
	// 的合理解压比远低于 5 倍；超过即按炸弹预检拒绝，解析器不再触碰字节）。
	maxArchiveUncompressed = 96 << 20
)

// MaxFileBytes 是单文件字节上限的导出别名：传输层入口（请求体限流）与
// 设施面共用同一常量口径。
const MaxFileBytes = maxFileBytes

// fileIdentity 是识别产物：sniffedMIME 是嗅探事实（落库 mime_type），ext
// 是原始扩展名（快速拒绝与风险比对用），route 是路由档位（FileRoute*），
// directKind 是纯文本档位的条目 kind（非 direct 档位为空）。
type fileIdentity struct {
	sniffedMIME string
	ext         string
	route       string
	directKind  string
}

// identifyFile 对上传文件做三层校验与路由分派。sniff 是内容嗅探函数
// （注入以便测试替身；生产为 sniffMIME）。拒绝语义：
//   - 扩展名 / 嗅探 MIME 不在白名单：ErrFileTypeUnsupported（入口层映射
//     FILE_TYPE_UNSUPPORTED）；
//   - 空文件、超 20MB：ErrDocumentInvalidArgument（完整性问题按入参非法
//     拒绝，与类型不支持区分）；
//   - zip 容器预检不过：ErrFileTypeUnsupported（容器事实与声明不符）。
func identifyFile(fileName string, body []byte, sniff func([]byte) string) (*fileIdentity, error) {
	// 第一层：扩展名白名单。
	ext := fileExtension(fileName)
	expected, ok := extensionMIME[ext]
	if !ok {
		return nil, fmt.Errorf("%w: extension %q is not ingestible", ErrFileTypeUnsupported, ext)
	}
	// 第三层的字节上限（先于嗅探执行）：空文件没有内容可识别，超限文件
	// 不为其做检测——都在识别期拒绝，字节不进对象存储。
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: file is empty", ErrDocumentInvalidArgument)
	}
	if len(body) > maxFileBytes {
		return nil, fmt.Errorf("%w: file size %d exceeds the %d byte limit", ErrDocumentInvalidArgument, len(body), maxFileBytes)
	}

	// 第二层：内容嗅探。嗅探事实是路由与落库的唯一依据。
	sniffed := normalizeMIME(sniff(body))
	route := fileMIMECategory(sniffed, ext)
	if route == FileRouteUnsupported {
		// 扩展名通过但内容不在白名单：改扩展名伪装或类型漂移，按不支持
		// 拒绝（文案带上嗅探事实供排障）。
		return nil, fmt.Errorf("%w: sniffed mime %q is not ingestible", ErrFileTypeUnsupported, sniffed)
	}

	identity := &fileIdentity{sniffedMIME: sniffed, ext: ext, route: route}
	if route == FileRouteDirect {
		identity.directKind = fileRouteForDirectType(ext)
	}

	// 扩展名与嗅探 MIME 不一致（嗅探回落通配 octet-stream 除外）记风险
	// 日志：不拒绝——magic bytes 嗅探是权威事实，file_name 保留原名、
	// mime_type 存嗅探结果，消费方按 MIME 处理。
	if !mimeMatchesExpectation(sniffed, expected) && sniffed != mimeOctetStream {
		slog.Warn("uploaded file extension disagrees with sniffed mime; keeping original name and sniffed mime",
			slog.String("file_name", fileName),
			slog.String("extension", ext),
			slog.String("sniffed_mime", sniffed))
	}

	// zip 容器预检：OOXML 三件套（xlsx / docx / pptx）无论走内档还是外部
	// 档都要设防——pptx 的字节会交给外部服务，更要在出进程前拒掉炸弹。
	if isArchiveType(sniffed, ext) {
		if err := precheckArchive(body); err != nil {
			return nil, err
		}
	}
	return identity, nil
}

// sniffMIME 是生产嗅探函数：mimetype 检测的薄包装（识别不出按
// octet-stream 上报，路由层再拒）。
func sniffMIME(body []byte) string {
	return mimetype.Detect(body).String()
}

// mimeMatchesExpectation 判定嗅探 MIME 是否命中扩展名的期望集合（前缀
// 匹配容纳 "text/html; charset=utf-8" 形态；入参均已归一化小写）。
func mimeMatchesExpectation(sniffed string, expected []string) bool {
	for _, want := range expected {
		if sniffed == want || len(sniffed) > len(want) && sniffed[:len(want)+1] == want+";" {
			return true
		}
	}
	return false
}

// isArchiveType 判定输入是否需要 zip 容器预检：嗅探或扩展名指向 OOXML
// 三件套任一即算（嗅探回落的 octet-stream 由扩展名兜住）。
func isArchiveType(sniffed, ext string) bool {
	switch normalizeMIME(sniffed) {
	case mimeXLSX, mimeDOCX, mimePPTX:
		return true
	}
	switch ext {
	case ".xlsx", ".docx", ".pptx":
		return true
	}
	return false
}

// precheckArchive 对 zip 容器做炸弹预检：读取中央目录，按 entry 数与声明
// 的解压总量设限。只读目录数据、不触碰任何 entry 内容——解析器（excelize
// / docx 手解）在此之后才打开容器，本函数是它们的独立防线；entry 声明值
// 与实际流不符的极端构造由解析侧的有界读取兜底（恶意内容根治靠外部档
// 独立进程隔离）。
func precheckArchive(body []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return fmt.Errorf("%w: not a readable zip container: %v", ErrFileTypeUnsupported, err)
	}
	if len(reader.File) > maxArchiveEntries {
		return fmt.Errorf("%w: zip container declares %d entries, max %d", ErrFileTypeUnsupported, len(reader.File), maxArchiveEntries)
	}
	var total uint64
	for _, f := range reader.File {
		total += f.UncompressedSize64
	}
	if total > maxArchiveUncompressed {
		return fmt.Errorf("%w: zip container declares %d uncompressed bytes, max %d", ErrFileTypeUnsupported, total, maxArchiveUncompressed)
	}
	return nil
}
