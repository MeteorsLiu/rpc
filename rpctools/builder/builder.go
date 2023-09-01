package builder

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"strings"
)

type Type struct {
	IsPointer bool
	IsArray   bool
	Name      string
}

func (t *Type) String() string {
	return fmt.Sprintf("isArray: %v isPtr: %v Name: %s", t.IsArray, t.IsPointer, t.Name)
}

type Name string
type Field map[Name]*Type

type AST struct {
	notPanic     bool
	filename     string
	exportStruct string
	rpcRepo      string
	fields       map[string]Field
}

func NewAST(f, export, rpcRepoPath string, notPanic ...bool) *AST {
	paniced := true
	if len(notPanic) > 0 {
		paniced = notPanic[0]
	}
	return &AST{
		rpcRepo:      rpcRepoPath,
		notPanic:     paniced,
		filename:     f,
		exportStruct: export,
	}
}

func isStruct(Type ast.Expr) (ok bool, s *ast.StructType) {
	ast.Inspect(Type, func(n ast.Node) bool {
		switch nx := n.(type) {
		case *ast.Ident:
			ok, s = getStruct(nx.Obj)
		}
		return true
	})
	return
}

func getStruct(obj *ast.Object) (ok bool, s *ast.StructType) {
	if obj == nil {
		return
	}

	switch o := obj.Decl.(type) {
	case *ast.TypeSpec:
		s, ok = o.Type.(*ast.StructType)
	case *ast.Field:
		s, ok = o.Type.(*ast.StructType)
	}
	return
}

func parseStruct(field Field, s *ast.StructType) Field {
	if field == nil {
		field = Field{}
	}
	for _, nx := range s.Fields.List {
		if nx.Names[0].IsExported() {
			switch n := nx.Type.(type) {
			case *ast.Ident:
				field[Name(nx.Names[0].Name)] = &Type{
					Name: n.Name,
				}
			case *ast.ArrayType:
				ast.Inspect(n.Elt, func(n ast.Node) bool {
					switch nn := n.(type) {
					case *ast.StarExpr:
						if oldnn, ok := field[Name(nx.Names[0].Name)]; ok {
							oldnn.IsPointer = true
						} else {
							field[Name(nx.Names[0].Name)] = &Type{}
						}
					case *ast.Ident:
						if oldnn, ok := field[Name(nx.Names[0].Name)]; ok {
							oldnn.Name = nn.Name
							oldnn.IsArray = true
						} else {
							field[Name(nx.Names[0].Name)] = &Type{
								Name:    nn.Name,
								IsArray: true,
							}
						}
					}
					return true
				})
			default:
				if isStruct, subField := isStruct(nx.Type); isStruct {
					parseStruct(field, subField)
				}
			}
		}
	}
	return field
}

func ParseStruct(obj ast.Expr) (bool, Field) {
	ok, s := isStruct(obj)
	field := Field{}
	if ok {
		field = parseStruct(field, s)
	}
	return ok, field
}

func (a *AST) Parse() *AST {
	fset := token.NewFileSet()
	fs, err := parser.ParseFile(fset, a.filename, nil, 0)
	if err != nil {
		log.Fatal("Parse Error: ", err)
	}
	rpcFunc := []*ast.FuncDecl{}
	ast.Inspect(fs, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			ast.Inspect(x.Recv, func(nested ast.Node) bool {
				switch nx := nested.(type) {
				case *ast.Ident:
					if nx.Name == a.exportStruct {
						rpcFunc = append(rpcFunc, x)
					}
				}
				return true
			})
		}
		return true
	})
	fields := map[string]Field{}
	for _, f := range rpcFunc {
		for _, nx := range f.Type.Params.List {
			ok, m := ParseStruct(nx.Type)
			if ok {
				fields[f.Name.Name] = m
			} else {
				if idt, ok := nx.Type.(*ast.Ident); ok {
					name := strings.ToUpper(nx.Names[0].Name[0:1]) + nx.Names[0].Name[1:]
					if old, ok := fields[f.Name.Name][Name(name)]; ok {
						if a.notPanic {
							log.Printf("RPC函数 %s 参数重复: %s 类型: %s 冲突类型: %s", f.Name.Name, name, idt.Name, old)
							return a
						} else {
							log.Fatalf("RPC函数 %s 参数重复: %s 类型: %s 冲突类型: %s", f.Name.Name, name, idt.Name, old)
						}
					}
					fields[f.Name.Name][Name(name)] = &Type{Name: idt.Name}
				}
			}

		}
	}
	a.fields = fields
	return a
}

func (a *AST) ToGIN() string {
	if len(a.fields) == 0 {
		log.Fatal("未完成文件解析")
	}
	var dispatcher strings.Builder
	var funcs strings.Builder
	var postFuncs strings.Builder

	Writef := func(f string, s ...any) {
		fmt.Fprintf(&dispatcher, f, s...)
	}

	WriteFuncf := func(f string, s ...any) {
		fmt.Fprintf(&funcs, f, s...)
	}

	WritePostFuncf := func(f string, s ...any) {
		fmt.Fprintf(&postFuncs, f, s...)
	}

	for methods, params := range a.fields {
		lowerMethod := strings.ToLower(methods)
		Writef(`
		r.GET("/%s", gg.%s)
		r.POST("/%s", gg.Post%s)`, lowerMethod, methods, lowerMethod, methods)

		WriteFuncf(`
	// THIS FUNCTION IS GENERATED, DON'T EDIT
	func (gg *GINGenerated) %s(g *gin.Context) {
		args := &rpcStruct.%s{}
		ret := map[string]any{}`, methods, a.exportStruct)

		WritePostFuncf(`
	// THIS FUNCTION IS GENERATED, DON'T EDIT
	func (gg *GINGenerated) Post%s(g *gin.Context) {
		args := &rpcStruct.%s{}
		ret := map[string]any{}
		b, err := g.GetRawData()
		if err != nil || len(b) == 0 {
			gg.abortContext(g, fmt.Sprintln("cannot get the data", err))
			return
		}
		if err := json.Unmarshal(b, args); err != nil {
			gg.abortContext(g, fmt.Sprintln("cannot unmarshal input data", err))
			return
		}
		gg.cli.CallAsync("%s.%s", args, &ret)
		g.JSON(http.StatusOK, gin.H{
			"status": "ok", 
			"callback": ret,
		})
	}
		
	`, methods, a.exportStruct, a.exportStruct, methods)

		for name, t := range params {
			if t.IsArray {
				continue
			}
			lowerName := strings.ToLower(string(name))

			WriteFuncf(`
		%s := g.Query("%s")
		if %s == "" {
			gg.abortContext(g, "empty value: %s")
			return
		}`, name, lowerName, name, name)

			switch t.Name {
			case "string":
				WriteFuncf(`
		args.%s = %s`, name, name)
			case "int64":
				WriteFuncf(`
		args.%s, _ = strconv.ParseInt(%s, 10, 64)`, name, name)
			case "uint64":
				WriteFuncf(`
		args.%s, _ = strconv.ParseUint(%s, 10, 64)`, name, name)
			case "byte":
				WriteFuncf(`
		tmp%s, _ := strconv.ParseUint(%s, 10, 8)
		args.%s = %s(tmp%s)`, name, name, name, t.Name, name)
			case "uint", "uint8", "uint16", "uint32", "uintptr":
				WriteFuncf(`
		tmp%s, _ := strconv.ParseUint(%s, 10, 0)
		args.%s = %s(tmp%s)`, name, name, name, t.Name, name)
			case "int", "int8", "int16", "int32", "intptr":
				WriteFuncf(`
		tmp%s, _ := strconv.ParseInt(%s, 10, 0)
		args.%s = %s(tmp%s)`, name, name, name, t.Name, name)
			case "float32":
				WriteFuncf(`
		tmp%s, _ := strconv.ParseFloat(%s, 32)
		args.%s = %s(tmp%s)`, name, name, name, t.Name, name)
			case "float64":
				WriteFuncf(`
		args.%s, _ = strconv.ParseFloat(%s, 64)`, name, name)
			case "complex64":
				WriteFuncf(`
		tmp%s, _ := strconv.ParseComplex(%s, 64)
		args.%s = %s(tmp%s)`, name, name, name, t.Name, name)
			case "complex128":
				WriteFuncf(`
		args.%s, _ = strconv.ParseComplex(%s, 128)`, name, name)
			case "bool":
				WriteFuncf(`
		switch %s {
		case "1", "true":
			args.%s = true
		default:
			args.%s = false
		}`, name, name, name)
			}

		}

		WriteFuncf(`
		gg.cli.CallAsync("%s.%s", args, &ret)
		g.JSON(http.StatusOK, gin.H{
			"status": "ok", 
			"callback": ret,
		})
	}
		`, a.exportStruct, methods)

	}

	return fmt.Sprintf(`
	package rpcgenerated
	// THIS FILE IS GENERATED BY rpctools/builders.go
	// DON'T EDIT
	// If you need to update contents of this file, re-generate it via rpctools.
	import (
		"fmt"
		"strconv"
		rpcStruct "%s"
		"github.com/MeteorsLiu/rpc/client"
		"github.com/gin-gonic/gin"
	)
	type Options func(*GINGenerated)

	func WithFailHandler(h gin.HandlerFunc) Options {
		return func(g *GINGenerated) {
			g.onFail = h
		}
	}

	func WithSuccessHandler(h gin.HandlerFunc) Options {
		return func(g *GINGenerated) {
			g.onSuccess = h
		}
	}

	type GINGenerated struct {
		cli 	  *client.Client
		onFail    gin.HandlerFunc
		onSuccess gin.HandlerFunc
	}

	func NewGINGenerated(cli *client.Client, r gin.IRoutes, opts ...Options) *GINGenerated {
		gg := &GINGenerated{cli: cli}
		for _, o := range opts {
			o(gg)	
		}
		%s
		return gg
	}

	func (gg *GINGenerated) abortContext(g *gin.Context, reason string) {
		switch {
		case reason != "" && gg.onFail != nil:
			gg.onFail(g)
		case reason == "" && gg.onSuccess != nil:
			gg.onSuccess(g)
		case reason != "":
			g.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status": "error", 
				"err": reason,
			})
		case reason == "":
			g.JSON(http.StatusOK, gin.H{
				"status": "ok", 
			})
		}
	}
	
	%s
	%s
	`, a.rpcRepo, dispatcher.String(), funcs.String(), postFuncs.String())
}
