package parser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
	css "github.com/tree-sitter/tree-sitter-css/bindings/go"
	html "github.com/tree-sitter/tree-sitter-html/bindings/go"
	javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	jsdoc "github.com/tree-sitter/tree-sitter-jsdoc/bindings/go"
	json "github.com/tree-sitter/tree-sitter-json/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type Language string

const (
	LangTypeScript Language = "typescript"
	LangTSX        Language = "tsx"
	LangJavaScript Language = "javascript"
	LangJSX        Language = "jsx"
	LangCSS        Language = "css"
	LangHTML       Language = "html"
	LangJSON       Language = "json"
	LangJSDoc      Language = "jsdoc"
	LangUnknown    Language = "unknown"
)

type Parser struct {
	parsers map[Language]*sitter.Parser
	mu      sync.Mutex
}

func New() *Parser {
	p := &Parser{
		parsers: make(map[Language]*sitter.Parser),
	}
	p.initLanguages()
	return p
}

func (p *Parser) initLanguages() {
	p.parsers[LangTypeScript] = newParser(typescript.LanguageTypescript())
	p.parsers[LangTSX] = newParser(typescript.LanguageTSX())

	jsParser := newParser(javascript.Language())
	p.parsers[LangJavaScript] = jsParser
	p.parsers[LangJSX] = jsParser

	p.parsers[LangCSS] = newParser(css.Language())
	p.parsers[LangHTML] = newParser(html.Language())
	p.parsers[LangJSON] = newParser(json.Language())
	p.parsers[LangJSDoc] = newParser(jsdoc.Language())
}

func newParser(lang unsafe.Pointer) *sitter.Parser {
	parser := sitter.NewParser()
	parser.SetLanguage(sitter.NewLanguage(lang))
	return parser
}

func DetectLanguage(filePath string) Language {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".ts":
		return LangTypeScript
	case ".tsx":
		return LangTSX
	case ".js", ".mjs", ".cjs":
		return LangJavaScript
	case ".jsx":
		return LangJSX
	case ".css", ".scss", ".less":
		return LangCSS
	case ".html", ".htm":
		return LangHTML
	case ".json":
		return LangJSON
	default:
		return LangUnknown
	}
}

func (p *Parser) ParseFile(ctx context.Context, filePath string) (*ParseResult, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return p.Parse(ctx, filePath, content)
}

func (p *Parser) Parse(ctx context.Context, filePath string, content []byte) (*ParseResult, error) {
	lang := DetectLanguage(filePath)
	if lang == LangUnknown {
		return nil, fmt.Errorf("unsupported file type: %s", filePath)
	}

	p.mu.Lock()
	parser, ok := p.parsers[lang]
	p.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("no parser for language: %s", lang)
	}

	p.mu.Lock()
	tree := parser.Parse(content, nil)
	p.mu.Unlock()

	if tree == nil {
		return nil, fmt.Errorf("failed to parse: tree is nil")
	}
	defer tree.Close()

	hash := sha256.Sum256(content)
	result := &ParseResult{
		FilePath: filePath,
		Language: string(lang),
		Hash:     hex.EncodeToString(hash[:]),
		Symbols:  make([]Symbol, 0),
		Imports:  make([]Import, 0),
		Errors:   make([]string, 0),
	}

	p.extractSymbols(tree.RootNode(), content, result, "")
	return result, nil
}

func (p *Parser) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, parser := range p.parsers {
		parser.Close()
	}
	p.parsers = nil
}
