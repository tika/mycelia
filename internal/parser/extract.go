package parser

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// ---------------------------------------------------------------------------
// AST Traversal
// ---------------------------------------------------------------------------

func (p *Parser) extractSymbols(node *sitter.Node, content []byte, result *ParseResult, parent string) {
	if node == nil {
		return
	}

	switch node.Kind() {
	case "import_statement":
		p.extractImport(node, content, result)

	case "function_declaration", "generator_function_declaration":
		if sym := p.extractFunction(node, content, parent); sym != nil {
			result.Symbols = append(result.Symbols, *sym)
		}

	case "lexical_declaration", "variable_declaration":
		p.extractVariableDeclarations(node, content, result, parent)

	case "class_declaration":
		if sym := p.extractClass(node, content); sym != nil {
			result.Symbols = append(result.Symbols, *sym)
		}

	case "interface_declaration":
		if sym := p.extractInterface(node, content, parent); sym != nil {
			result.Symbols = append(result.Symbols, *sym)
		}

	case "type_alias_declaration":
		if sym := p.extractTypeAlias(node, content, parent); sym != nil {
			result.Symbols = append(result.Symbols, *sym)
		}

	case "enum_declaration":
		if sym := p.extractEnum(node, content, parent); sym != nil {
			result.Symbols = append(result.Symbols, *sym)
		}

	case "export_statement":
		p.extractExport(node, content, result, parent)

	default:
		for i := uint(0); i < node.ChildCount(); i++ {
			p.extractSymbols(node.Child(i), content, result, parent)
		}
	}
}

// ---------------------------------------------------------------------------
// Imports
// ---------------------------------------------------------------------------

func (p *Parser) extractImport(node *sitter.Node, content []byte, result *ParseResult) {
	imp := Import{
		StartByte: node.StartByte(),
		EndByte:   node.EndByte(),
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "string":
			imp.Source = trimQuotes(nodeText(child, content))
		case "import_clause":
			p.extractImportClause(child, content, &imp)
		case "namespace_import":
			if nameNode := child.ChildByFieldName("name"); nameNode != nil {
				imp.Alias = nodeText(nameNode, content)
			}
		}
	}

	result.Imports = append(result.Imports, imp)
}

func (p *Parser) extractImportClause(node *sitter.Node, content []byte, imp *Import) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "identifier":
			// import Foo from "..."
			imp.Names = append(imp.Names, nodeText(child, content))
			imp.IsDefault = true

		case "named_imports":
			// import { a, b, c } from "..."
			for j := uint(0); j < child.ChildCount(); j++ {
				spec := child.Child(j)
				if spec.Kind() == "import_specifier" {
					if nameNode := spec.ChildByFieldName("name"); nameNode != nil {
						imp.Names = append(imp.Names, nodeText(nameNode, content))
					}
				}
			}

		case "namespace_import":
			// import * as X from "..."
			for j := uint(0); j < child.ChildCount(); j++ {
				if child.Child(j).Kind() == "identifier" {
					imp.Alias = nodeText(child.Child(j), content)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Functions & Variables
// ---------------------------------------------------------------------------

func (p *Parser) extractFunction(node *sitter.Node, content []byte, parent string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	sym := &Symbol{
		Name:       nodeText(nameNode, content),
		Kind:       SymbolFunction,
		StartByte:  node.StartByte(),
		EndByte:    node.EndByte(),
		StartLine:  node.StartPosition().Row,
		EndLine:    node.EndPosition().Row,
		Parent:     parent,
		IsExported: p.isExported(node),
	}

	if params := node.ChildByFieldName("parameters"); params != nil {
		sym.Signature = nodeText(params, content)
	}

	return sym
}

func (p *Parser) extractVariableDeclarations(node *sitter.Node, content []byte, result *ParseResult, parent string) {
	isConst := false
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "const" {
			isConst = true
		}
		if child.Kind() == "variable_declarator" {
			p.extractVariableDeclarator(child, content, result, parent, isConst)
		}
	}
}

func (p *Parser) extractVariableDeclarator(node *sitter.Node, content []byte, result *ParseResult, parent string, isConst bool) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}

	name := nodeText(nameNode, content)
	valueNode := node.ChildByFieldName("value")

	// Check if RHS is a function (arrow function, function expression)
	if valueNode != nil {
		switch valueNode.Kind() {
		case "arrow_function", "function", "function_expression":
			// Use parent node (the declaration) for byte range so we capture "const foo = ..."
			declNode := node.Parent()
			sym := &Symbol{
				Name:      name,
				Kind:      SymbolFunction,
				StartByte: declNode.StartByte(),
				EndByte:   declNode.EndByte(),
				StartLine: declNode.StartPosition().Row,
				EndLine:   declNode.EndPosition().Row,
				Parent:    parent,
			}

			// "parameters" for arrow with parens, "parameter" for single param without parens
			if params := valueNode.ChildByFieldName("parameters"); params != nil {
				sym.Signature = nodeText(params, content)
			} else if param := valueNode.ChildByFieldName("parameter"); param != nil {
				sym.Signature = nodeText(param, content)
			}

			result.Symbols = append(result.Symbols, *sym)
			return
		}
	}

	// Non-function const
	if isConst {
		declNode := node.Parent()
		result.Symbols = append(result.Symbols, Symbol{
			Name:      name,
			Kind:      SymbolConstant,
			StartByte: declNode.StartByte(),
			EndByte:   declNode.EndByte(),
			StartLine: declNode.StartPosition().Row,
			EndLine:   declNode.EndPosition().Row,
			Parent:    parent,
		})
	}
}

// ---------------------------------------------------------------------------
// Classes
// ---------------------------------------------------------------------------

func (p *Parser) extractClass(node *sitter.Node, content []byte) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	className := nodeText(nameNode, content)
	sym := &Symbol{
		Name:       className,
		Kind:       SymbolClass,
		StartByte:  node.StartByte(),
		EndByte:    node.EndByte(),
		StartLine:  node.StartPosition().Row,
		EndLine:    node.EndPosition().Row,
		IsExported: p.isExported(node),
		Children:   make([]Symbol, 0),
	}

	body := node.ChildByFieldName("body")
	if body == nil {
		return sym
	}

	for i := uint(0); i < body.ChildCount(); i++ {
		member := body.Child(i)
		switch member.Kind() {
		case "method_definition":
			if method := p.extractMethod(member, content, className); method != nil {
				sym.Children = append(sym.Children, *method)
			}
		case "public_field_definition", "field_definition":
			if prop := p.extractProperty(member, content, className); prop != nil {
				sym.Children = append(sym.Children, *prop)
			}
		}
	}

	return sym
}

func (p *Parser) extractMethod(node *sitter.Node, content []byte, className string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	sym := &Symbol{
		Name:      nodeText(nameNode, content),
		Kind:      SymbolMethod,
		StartByte: node.StartByte(),
		EndByte:   node.EndByte(),
		StartLine: node.StartPosition().Row,
		EndLine:   node.EndPosition().Row,
		Parent:    className,
	}

	if params := node.ChildByFieldName("parameters"); params != nil {
		sym.Signature = nodeText(params, content)
	}

	return sym
}

func (p *Parser) extractProperty(node *sitter.Node, content []byte, className string) *Symbol {
	nameNode := node.ChildByFieldName("name")

	// Fallback: some grammars don't use field names for properties
	if nameNode == nil {
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Kind() == "property_identifier" || child.Kind() == "identifier" {
				nameNode = child
				break
			}
		}
	}

	if nameNode == nil {
		return nil
	}

	return &Symbol{
		Name:      nodeText(nameNode, content),
		Kind:      SymbolProperty,
		StartByte: node.StartByte(),
		EndByte:   node.EndByte(),
		StartLine: node.StartPosition().Row,
		EndLine:   node.EndPosition().Row,
		Parent:    className,
	}
}

// ---------------------------------------------------------------------------
// TypeScript-specific: Interfaces, Type Aliases, Enums
// ---------------------------------------------------------------------------

func (p *Parser) extractInterface(node *sitter.Node, content []byte, parent string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	sym := &Symbol{
		Name:       nodeText(nameNode, content),
		Kind:       SymbolInterface,
		StartByte:  node.StartByte(),
		EndByte:    node.EndByte(),
		StartLine:  node.StartPosition().Row,
		EndLine:    node.EndPosition().Row,
		Parent:     parent,
		IsExported: p.isExported(node),
		Children:   make([]Symbol, 0),
	}

	body := node.ChildByFieldName("body")
	if body == nil {
		return sym
	}

	for i := uint(0); i < body.ChildCount(); i++ {
		member := body.Child(i)
		if member.Kind() != "property_signature" && member.Kind() != "method_signature" {
			continue
		}

		nameChild := member.ChildByFieldName("name")
		if nameChild == nil {
			continue
		}

		kind := SymbolProperty
		if member.Kind() == "method_signature" {
			kind = SymbolMethod
		}

		sym.Children = append(sym.Children, Symbol{
			Name:      nodeText(nameChild, content),
			Kind:      kind,
			StartByte: member.StartByte(),
			EndByte:   member.EndByte(),
			StartLine: member.StartPosition().Row,
			EndLine:   member.EndPosition().Row,
			Parent:    sym.Name,
		})
	}

	return sym
}

func (p *Parser) extractTypeAlias(node *sitter.Node, content []byte, parent string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	return &Symbol{
		Name:       nodeText(nameNode, content),
		Kind:       SymbolType,
		StartByte:  node.StartByte(),
		EndByte:    node.EndByte(),
		StartLine:  node.StartPosition().Row,
		EndLine:    node.EndPosition().Row,
		Parent:     parent,
		IsExported: p.isExported(node),
	}
}

func (p *Parser) extractEnum(node *sitter.Node, content []byte, parent string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	return &Symbol{
		Name:       nodeText(nameNode, content),
		Kind:       SymbolEnum,
		StartByte:  node.StartByte(),
		EndByte:    node.EndByte(),
		StartLine:  node.StartPosition().Row,
		EndLine:    node.EndPosition().Row,
		Parent:     parent,
		IsExported: p.isExported(node),
	}
}

// ---------------------------------------------------------------------------
// Exports
// ---------------------------------------------------------------------------

func (p *Parser) extractExport(node *sitter.Node, content []byte, result *ParseResult, parent string) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)

		var sym *Symbol
		switch child.Kind() {
		case "function_declaration", "generator_function_declaration":
			sym = p.extractFunction(child, content, parent)
		case "class_declaration":
			sym = p.extractClass(child, content)
		case "interface_declaration":
			sym = p.extractInterface(child, content, parent)
		case "type_alias_declaration":
			sym = p.extractTypeAlias(child, content, parent)
		case "enum_declaration":
			sym = p.extractEnum(child, content, parent)
		case "lexical_declaration", "variable_declaration":
			p.extractVariableDeclarations(child, content, result, parent)
			if len(result.Symbols) > 0 {
				result.Symbols[len(result.Symbols)-1].IsExported = true
			}
			continue
		default:
			p.extractSymbols(child, content, result, parent)
			continue
		}

		if sym != nil {
			sym.IsExported = true
			result.Symbols = append(result.Symbols, *sym)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (p *Parser) isExported(node *sitter.Node) bool {
	if parent := node.Parent(); parent != nil && parent.Kind() == "export_statement" {
		return true
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		if node.Child(i).Kind() == "export" {
			return true
		}
	}

	return false
}

func nodeText(node *sitter.Node, content []byte) string {
	return string(content[node.StartByte():node.EndByte()])
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == s[len(s)-1] {
		switch s[0] {
		case '"', '\'', '`':
			return s[1 : len(s)-1]
		}
	}
	return s
}
