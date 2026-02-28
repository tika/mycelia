package parser

type SymbolKind int

const (
	SymbolFunction SymbolKind = iota
	SymbolMethod
	SymbolClass
	SymbolInterface
	SymbolStruct
	SymbolType
	SymbolVariable
	SymbolConstant
	SymbolImport
	SymbolExport
	SymbolModule
	SymbolEnum
	SymbolProperty
)

func (k SymbolKind) String() string {
	switch k {
	case SymbolFunction:
		return "function"
	case SymbolMethod:
		return "method"
	case SymbolClass:
		return "class"
	case SymbolInterface:
		return "interface"
	case SymbolStruct:
		return "struct"
	case SymbolType:
		return "type"
	case SymbolVariable:
		return "variable"
	case SymbolConstant:
		return "constant"
	case SymbolImport:
		return "import"
	case SymbolExport:
		return "export"
	case SymbolModule:
		return "module"
	case SymbolEnum:
		return "enum"
	case SymbolProperty:
		return "property"
	default:
		return "unknown"
	}
}

type Symbol struct {
	Name       string
	Kind       SymbolKind
	StartByte  uint
	EndByte    uint
	StartLine  uint
	EndLine    uint
	Parent     string
	Signature  string
	IsExported bool
	Children   []Symbol
}

type Import struct {
	Source    string
	Names     []string
	Alias     string
	IsDefault bool
	StartByte uint
	EndByte   uint
}

type ParseResult struct {
	FilePath string
	Language string
	Symbols  []Symbol
	Imports  []Import
	Hash     string
	Errors   []string
}
