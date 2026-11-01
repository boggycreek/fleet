// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package codeintel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"os"
	"strings"
)

// NativeGoDriver implements CodeIntelligenceProvider using the Go standard library AST parsers.
type NativeGoDriver struct{}

// NewNativeGoDriver constructs a new native Go AST code intelligence driver.
func NewNativeGoDriver() *NativeGoDriver {
	return &NativeGoDriver{}
}

// Name returns the provider driver name.
func (d *NativeGoDriver) Name() string {
	return "native-go"
}

// Languages returns supported file extensions.
func (d *NativeGoDriver) Languages() []string {
	return []string{".go", "go"}
}

// GetOutline extracts a hierarchical symbol tree from Go source code.
func (d *NativeGoDriver) GetOutline(_ context.Context, filePath string, content []byte) (*Outline, error) {
	if len(content) == 0 && filePath != "" {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read source file: %w", err)
		}
	}

	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Go AST: %w", err)
	}

	outline := &Outline{
		FilePath:    filePath,
		Language:    "go",
		PackageName: fileNode.Name.Name,
		Symbols:     make([]SymbolNode, 0),
	}

	for _, decl := range fileNode.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			doc := ""
			if d.Doc != nil {
				doc = d.Doc.Text()
			}
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.ImportSpec:
					startPos := fset.Position(s.Pos())
					endPos := fset.Position(s.End())
					importPath := ""
					if s.Path != nil {
						importPath = strings.Trim(s.Path.Value, `"`)
					}
					alias := ""
					if s.Name != nil {
						alias = s.Name.Name + " "
					}
					outline.Symbols = append(outline.Symbols, SymbolNode{
						Name:      importPath,
						Kind:      SymbolKindPackage,
						Signature: "import " + alias + `"` + importPath + `"`,
						Line:      startPos.Line,
						Col:       startPos.Column,
						EndLine:   endPos.Line,
						EndCol:    endPos.Column,
					})

				case *ast.TypeSpec:
					startPos := fset.Position(s.Pos())
					endPos := fset.Position(s.End())
					specDoc := doc
					if s.Doc != nil {
						specDoc = s.Doc.Text()
					}
					kind := SymbolKindType
					var children []SymbolNode
					sig := "type " + s.Name.Name

					switch t := s.Type.(type) {
					case *ast.StructType:
						kind = SymbolKindStruct
						sig = "type " + s.Name.Name + " struct"
						if t.Fields != nil {
							for _, field := range t.Fields.List {
								fStart := fset.Position(field.Pos())
								fEnd := fset.Position(field.End())
								fDoc := ""
								if field.Doc != nil {
									fDoc = field.Doc.Text()
								}
								var typeBuf bytes.Buffer
								_ = printer.Fprint(&typeBuf, fset, field.Type)
								fieldType := typeBuf.String()

								if len(field.Names) == 0 {
									// Embedded field
									children = append(children, SymbolNode{
										Name:      fieldType,
										Kind:      SymbolKindVariable,
										Signature: fieldType,
										Doc:       fDoc,
										Line:      fStart.Line,
										Col:       fStart.Column,
										EndLine:   fEnd.Line,
										EndCol:    fEnd.Column,
									})
								} else {
									for _, fn := range field.Names {
										children = append(children, SymbolNode{
											Name:      fn.Name,
											Kind:      SymbolKindVariable,
											Signature: fn.Name + " " + fieldType,
											Doc:       fDoc,
											Line:      fStart.Line,
											Col:       fStart.Column,
											EndLine:   fEnd.Line,
											EndCol:    fEnd.Column,
										})
									}
								}
							}
						}

					case *ast.InterfaceType:
						kind = SymbolKindInterface
						sig = "type " + s.Name.Name + " interface"
						if t.Methods != nil {
							for _, m := range t.Methods.List {
								mStart := fset.Position(m.Pos())
								mEnd := fset.Position(m.End())
								mDoc := ""
								if m.Doc != nil {
									mDoc = m.Doc.Text()
								}
								var mBuf bytes.Buffer
								_ = printer.Fprint(&mBuf, fset, m.Type)
								mType := mBuf.String()

								if len(m.Names) == 0 {
									// Embedded interface
									children = append(children, SymbolNode{
										Name:      mType,
										Kind:      SymbolKindInterface,
										Signature: mType,
										Doc:       mDoc,
										Line:      mStart.Line,
										Col:       mStart.Column,
										EndLine:   mEnd.Line,
										EndCol:    mEnd.Column,
									})
								} else {
									for _, mn := range m.Names {
										children = append(children, SymbolNode{
											Name:      mn.Name,
											Kind:      SymbolKindMethod,
											Signature: mn.Name + strings.TrimPrefix(mType, "func"),
											Doc:       mDoc,
											Line:      mStart.Line,
											Col:       mStart.Column,
											EndLine:   mEnd.Line,
											EndCol:    mEnd.Column,
										})
									}
								}
							}
						}

					default:
						var typeBuf bytes.Buffer
						_ = printer.Fprint(&typeBuf, fset, s.Type)
						sig = "type " + s.Name.Name + " " + typeBuf.String()
					}

					outline.Symbols = append(outline.Symbols, SymbolNode{
						Name:      s.Name.Name,
						Kind:      kind,
						Signature: sig,
						Doc:       specDoc,
						Line:      startPos.Line,
						Col:       startPos.Column,
						EndLine:   endPos.Line,
						EndCol:    endPos.Column,
						Children:  children,
					})

				case *ast.ValueSpec:
					startPos := fset.Position(s.Pos())
					endPos := fset.Position(s.End())
					specDoc := doc
					if s.Doc != nil {
						specDoc = s.Doc.Text()
					}
					kind := SymbolKindVariable
					if d.Tok == token.CONST {
						kind = SymbolKindConstant
					}
					for _, name := range s.Names {
						var valBuf bytes.Buffer
						if s.Type != nil {
							_ = printer.Fprint(&valBuf, fset, s.Type)
						}
						sig := string(kind) + " " + name.Name
						if valBuf.Len() > 0 {
							sig += " " + valBuf.String()
						}
						outline.Symbols = append(outline.Symbols, SymbolNode{
							Name:      name.Name,
							Kind:      kind,
							Signature: sig,
							Doc:       specDoc,
							Line:      startPos.Line,
							Col:       startPos.Column,
							EndLine:   endPos.Line,
							EndCol:    endPos.Column,
						})
					}
				}
			}

		case *ast.FuncDecl:
			startPos := fset.Position(d.Pos())
			endPos := fset.Position(d.End())
			doc := ""
			if d.Doc != nil {
				doc = d.Doc.Text()
			}

			declCopy := *d
			declCopy.Body = nil
			declCopy.Doc = nil
			var sigBuf bytes.Buffer
			_ = printer.Fprint(&sigBuf, fset, &declCopy)

			kind := SymbolKindFunction
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = SymbolKindMethod
			}

			outline.Symbols = append(outline.Symbols, SymbolNode{
				Name:      d.Name.Name,
				Kind:      kind,
				Signature: sigBuf.String(),
				Doc:       doc,
				Line:      startPos.Line,
				Col:       startPos.Column,
				EndLine:   endPos.Line,
				EndCol:    endPos.Column,
			})
		}
	}

	return outline, nil
}

// ValidateSyntax parses the Go code and surfaces all scanner and parser syntax diagnostics.
func (d *NativeGoDriver) ValidateSyntax(_ context.Context, filePath string, content []byte) ([]Diagnostic, error) {
	if len(content) == 0 && filePath != "" {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read source file: %w", err)
		}
	}

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, filePath, content, parser.AllErrors)
	if err == nil {
		return []Diagnostic{}, nil
	}

	var diags []Diagnostic
	var errList scanner.ErrorList
	if errors.As(err, &errList) {
		for _, e := range errList {
			diags = append(diags, Diagnostic{
				Message:  e.Msg,
				Severity: SeverityError,
				Line:     e.Pos.Line,
				Col:      e.Pos.Column,
				Rule:     "syntax-error",
			})
		}
		return diags, nil
	}

	var sErr scanner.Error
	if errors.As(err, &sErr) {
		diags = append(diags, Diagnostic{
			Message:  sErr.Msg,
			Severity: SeverityError,
			Line:     sErr.Pos.Line,
			Col:      sErr.Pos.Column,
			Rule:     "syntax-error",
		})
		return diags, nil
	}

	diags = append(diags, Diagnostic{
		Message:  err.Error(),
		Severity: SeverityError,
		Line:     1,
		Col:      1,
		Rule:     "syntax-error",
	})
	return diags, nil
}

// FindDefinition resolves the definition position of the symbol at the given coordinate.
func (d *NativeGoDriver) FindDefinition(_ context.Context, filePath string, line, col int) ([]Location, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source file: %w", err)
	}

	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Go AST: %w", err)
	}

	var targetIdent *ast.Ident
	ast.Inspect(fileNode, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			pos := fset.Position(id.Pos())
			end := fset.Position(id.End())
			if pos.Line == line && col >= pos.Column && col <= end.Column {
				targetIdent = id
				return false
			}
		}
		return true
	})

	if targetIdent == nil {
		return []Location{}, nil
	}

	if targetIdent.Obj != nil && targetIdent.Obj.Pos().IsValid() {
		pos := fset.Position(targetIdent.Obj.Pos())
		return []Location{{
			FilePath: filePath,
			Line:     pos.Line,
			Col:      pos.Column,
			EndLine:  pos.Line,
			EndCol:   pos.Column + len(targetIdent.Obj.Name),
		}}, nil
	}

	// Search file top-level declarations
	var locs []Location
	for _, decl := range fileNode.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == targetIdent.Name {
				pos := fset.Position(d.Name.Pos())
				locs = append(locs, Location{
					FilePath: filePath,
					Line:     pos.Line,
					Col:      pos.Column,
					EndLine:  pos.Line,
					EndCol:   pos.Column + len(d.Name.Name),
				})
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == targetIdent.Name {
					pos := fset.Position(ts.Name.Pos())
					locs = append(locs, Location{
						FilePath: filePath,
						Line:     pos.Line,
						Col:      pos.Column,
						EndLine:  pos.Line,
						EndCol:   pos.Column + len(ts.Name.Name),
					})
				}
			}
		}
	}

	return locs, nil
}

// FindReferences locates occurrences of the symbol at the given coordinate within the file.
func (d *NativeGoDriver) FindReferences(_ context.Context, filePath string, line, col int) ([]Location, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source file: %w", err)
	}

	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Go AST: %w", err)
	}

	var targetName string
	ast.Inspect(fileNode, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			pos := fset.Position(id.Pos())
			end := fset.Position(id.End())
			if pos.Line == line && col >= pos.Column && col <= end.Column {
				targetName = id.Name
				return false
			}
		}
		return true
	})

	if targetName == "" {
		return []Location{}, nil
	}

	var locs []Location
	ast.Inspect(fileNode, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == targetName {
			pos := fset.Position(id.Pos())
			end := fset.Position(id.End())
			locs = append(locs, Location{
				FilePath: filePath,
				Line:     pos.Line,
				Col:      pos.Column,
				EndLine:  end.Line,
				EndCol:   end.Column,
			})
		}
		return true
	})

	return locs, nil
}
