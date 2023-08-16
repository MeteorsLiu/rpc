package builder

type ExportStruct struct {
	A string
}

type NotExportStruct struct {
	B string
}
type FuncA_Args struct {
	a  string
	BB string
	CD *FuncC_Args
	CF []*FuncC_Args
	AA []string
	BG complex128
	AB float64
	CC byte
	CB complex64
	CT float32
	CQ bool
	CN uint64
}
type FuncB_Args struct {
	b string
}

type FuncC_Args struct {
	c  string
	DA int
}

func (e *ExportStruct) FuncA(a *FuncA_Args, b string, c int) {

}

func (e *ExportStruct) FuncB(b *FuncB_Args, c string, d int) {

}

func (e *ExportStruct) FuncC(c *FuncC_Args) {

}

func (e ExportStruct) FuncNotPtr(c *FuncC_Args) {

}

func (n NotExportStruct) FuncNotPtr(c *FuncC_Args) {

}

func (n *NotExportStruct) FuncPtr(c *FuncC_Args) {

}
