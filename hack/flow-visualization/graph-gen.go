package main

import (
	"bytes"
	"flag"
	"fmt"
	"hash/adler32"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"unicode"

	shootOperation "github.com/gardener/gardener/pkg/controllermanager/controller/shoot"
	"github.com/gardener/gardener/pkg/operation/botanist"
	"github.com/gardener/gardener/pkg/utils/flow"

	"gonum.org/v1/gonum/graph/encoding"
	"gonum.org/v1/gonum/graph/encoding/dot"
	"gonum.org/v1/gonum/graph/simple"
)

type dotAttributes map[string]string

type graphWithAttributes struct {
	*simple.DirectedGraph
	name            string
	graphAttributes dotAttributes
	nodeAttributes  dotAttributes
	edgeAttributes  dotAttributes
}

func (g *graphWithAttributes) Name() string {
	return g.name
}

func (g *graphWithAttributes) Add(task flow.Task) flow.TaskID {
	newNode := NewNodeWithAttributes(task.Name)
	stringID := strconv.Itoa(int(newNode.ID()))

	taskFuncPtr := reflect.ValueOf(task.Fn).Pointer()
	emptyFuncPtr := reflect.ValueOf(flow.EmptyTaskFn).Pointer()
	if taskFuncPtr == emptyFuncPtr {
		newNode.skipped = true
		newNode.attributes["fillcolor"] = "lightgrey"
		newNode.attributes["style"] = g.nodeAttributes["style"] + ",dashed"
		newNode.attributes["group"] = "optional"
	} else {
		newNode.attributes["group"] = "mandatory"
	}

	g.AddNode(newNode)

	spec := task.Spec()
	for dependencyStringID := range spec.Dependencies {
		newNode.edgeCountIn++

		dependencyID, err := strconv.Atoi(string(dependencyStringID))
		if err != nil {
			panic(fmt.Errorf("could not parse dependecyID: %+v", err))
		}

		dependencyNode := g.Node(int64(dependencyID)).(*nodeWithAttributes)
		if dependencyNode == nil {
			fmt.Println("could not find dependency node")
			continue
		}

		dependencyNode.edgeCountOut++

		edge := NewEdgeWithAttributes(simple.Edge{F: dependencyNode, T: newNode})

		if newNode.skipped || dependencyNode.skipped {
			//edge.attributes["style"] = "dashed"
		} else {
			//edge.attributes["group"] = "mandatory"
		}

		g.SetEdge(edge)
	}

	return flow.TaskID(stringID)
}

func (g *graphWithAttributes) Compile() *flow.Flow {
	return nil
}

func NewGraphWithAttributes(name string) *graphWithAttributes {
	return &graphWithAttributes{
		DirectedGraph: simple.NewDirectedGraph(),
		name:          name,
		graphAttributes: dotAttributes{
			"label":    name,
			"labelloc": "t",
			//"size":     "25,50",
			//"ratio": "auto",
			//"page":  "8.5,11",
			"fontname": "Helvetica",
			"fontsize": "18",
			//"ranksep":  "1",
		},
		nodeAttributes: dotAttributes{
			"shape":     "box",
			"fontname":  "Helvetica",
			"fontsize":  "14",
			"style":     "filled",
			"fillcolor": "lightskyblue1",
		},
		edgeAttributes: dotAttributes{
		},
	}
}

func (m dotAttributes) Attributes() []encoding.Attribute {
	out := make([]encoding.Attribute, 0, len(m))

	for k, v := range m {
		out = append(out, encoding.Attribute{
			Key:   k,
			Value: v,
		})
	}

	return out
}

func (g *graphWithAttributes) DOTAttributers() (graph, node, edge encoding.Attributer) {
	return g.graphAttributes, g.nodeAttributes, g.edgeAttributes
}

type nodeWithAttributes struct {
	simple.Node
	attributes   dotAttributes
	skipped      bool
	edgeCountIn  uint
	edgeCountOut uint
}

func (n *nodeWithAttributes) Attributes() []encoding.Attribute {
	return n.attributes.Attributes()
}

func NewNodeWithAttributes(label string) *nodeWithAttributes {
	return &nodeWithAttributes{
		Node: simple.Node(hashString(label)),
		attributes: dotAttributes{
			"label": WrapString(label, 20),
		},
	}
}

type edgeWithAttributes struct {
	simple.Edge
	attributes dotAttributes
}

func (e *edgeWithAttributes) Attributes() []encoding.Attribute {
	return NewCompoundAttributer(e.attributes, dotAttributes{
		//"weight": fmt.Sprintf("%d", e.Weight()),
	}).Attributes()
}

func (e *edgeWithAttributes) Weight() uint {
	f := e.Edge.From().(*nodeWithAttributes)
	t := e.Edge.To().(*nodeWithAttributes)

	return f.edgeCountOut + t.edgeCountIn
}

func NewEdgeWithAttributes(edge simple.Edge) *edgeWithAttributes {
	return &edgeWithAttributes{
		Edge:       edge,
		attributes: dotAttributes{},
	}
}

type compoundAttributer struct {
	attributers []encoding.Attributer
}

func (c *compoundAttributer) Attributes() []encoding.Attribute {
	out := make([]encoding.Attribute, 0)

	for _, attributer := range c.attributers {
		out = append(out, attributer.Attributes()...)
	}

	return out
}

func NewCompoundAttributer(attributers ...encoding.Attributer) *compoundAttributer {
	return &compoundAttributer{attributers: attributers}
}

// WrapString wraps the given string within lim width in characters.
func WrapString(s string, lim uint) string {
	// Initialize a buffer with a slightly larger size to account for breaks
	init := make([]byte, 0, len(s))
	buf := bytes.NewBuffer(init)

	var current uint
	var wordBuf, spaceBuf bytes.Buffer

	for _, char := range s {
		if char == '\n' {
			if wordBuf.Len() == 0 {
				if current+uint(spaceBuf.Len()) > lim {
					current = 0
				} else {
					current += uint(spaceBuf.Len())
					spaceBuf.WriteTo(buf)
				}
				spaceBuf.Reset()
			} else {
				current += uint(spaceBuf.Len() + wordBuf.Len())
				spaceBuf.WriteTo(buf)
				spaceBuf.Reset()
				wordBuf.WriteTo(buf)
				wordBuf.Reset()
			}
			buf.WriteRune(char)
			current = 0
		} else if unicode.IsSpace(char) {
			if spaceBuf.Len() == 0 || wordBuf.Len() > 0 {
				current += uint(spaceBuf.Len() + wordBuf.Len())
				spaceBuf.WriteTo(buf)
				spaceBuf.Reset()
				wordBuf.WriteTo(buf)
				wordBuf.Reset()
			}

			spaceBuf.WriteRune(char)
		} else {
			wordBuf.WriteRune(char)

			if current+uint(spaceBuf.Len()+wordBuf.Len()) > lim && uint(wordBuf.Len()) < lim {
				buf.WriteRune('\n')
				current = 0
				spaceBuf.Reset()
			}
		}
	}

	if wordBuf.Len() == 0 {
		if current+uint(spaceBuf.Len()) <= lim {
			spaceBuf.WriteTo(buf)
		}
	} else {
		spaceBuf.WriteTo(buf)
		wordBuf.WriteTo(buf)
	}

	return buf.String()
}

func hashString(s string) int64 {
	return int64(adler32.Checksum([]byte(s)))
}

func main() {
	outputFile := flag.String("output", "graph-gen.gv", "Output file")
	flag.Parse()

	if outputFile == nil {
		panic("flag output not defined")
	}

	if !filepath.IsAbs(*outputFile) {
		if pwd, err := os.Getwd(); err != nil {
			panic(err)
		} else {
			*outputFile = filepath.Join(pwd, *outputFile)
		}
	}

	file, err := os.Create(*outputFile)
	if err != nil {
		panic(err)
	}

	defer func() {
		if err = file.Close(); err != nil {
			panic(err)
		}
	}()

	g := NewGraphWithAttributes("Shoot cluster reconciliation")
	o := &fakeOperation{
		isShootHibernationEnabled:     false,
		isSeedBackupEnabled:           false,
		isShootExternalDomainManaged:  false,
		isGardenInternalDomainManaged: false,
	}

	shootOperation.AddReconcileShootFlowTasks(g, o, &botanist.Botanist{}, true)

	result, err := dot.Marshal(g, "", "", "  ")
	if err != nil {
		panic(err)
	}

	if _, err := fmt.Fprintf(file, "%s\n", result); err != nil {
		panic(err)
	}
}

type fakeOperation struct {
	isShootHibernationEnabled     bool
	isSeedBackupEnabled           bool
	isShootExternalDomainManaged  bool
	isGardenInternalDomainManaged bool
}

func (f *fakeOperation) IsShootHibernationEnabled() bool {
	return f.isShootHibernationEnabled
}
func (f *fakeOperation) IsSeedBackupEnabled() bool {
	return f.isSeedBackupEnabled
}
func (f *fakeOperation) IsShootExternalDomainManaged() bool {
	return f.isShootExternalDomainManaged
}
func (f *fakeOperation) IsGardenInternalDomainManaged() bool {
	return f.isGardenInternalDomainManaged
}
