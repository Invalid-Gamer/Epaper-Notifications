package template

import (
	"sort"
	"text/template/parse"
)

// ref ist eine im Template verwendete Variable mit der ersten Fundstelle.
type ref struct {
	name string
	loc  string // "datei.tmpl:7:12"
}

// referencedFields ermittelt alle Variablen der obersten Ebene, die ein Template
// verwendet (.name und $.name). So fällt ein Tippfehler wie {{.usr_name}} beim
// Start auf und nicht erst, wenn eine Mail leer rausgeht.
//
// Innerhalb von {{range}} und {{with}} zeigt der Punkt auf ein anderes Objekt;
// Felder wie {{.name}} bezeichnen dort Elemente und werden nicht geprüft.
// {{define}}-Blöcke werden nicht analysiert.
func referencedFields(tree *parse.Tree) []ref {
	if tree == nil || tree.Root == nil {
		return nil
	}
	w := &walker{tree: tree, first: map[string]string{}}
	w.node(tree.Root, true)

	out := make([]ref, 0, len(w.first))
	for name, loc := range w.first {
		out = append(out, ref{name: name, loc: loc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

type walker struct {
	tree  *parse.Tree
	first map[string]string // Name -> erste Fundstelle
}

func (w *walker) record(name string, n parse.Node) {
	if _, seen := w.first[name]; !seen {
		loc, _ := w.tree.ErrorContext(n)
		w.first[name] = loc
	}
}

// node durchläuft den Baum. rootDot ist true, solange "." noch die Eingabedaten sind.
func (w *walker) node(n parse.Node, rootDot bool) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			w.node(c, rootDot)
		}
	case *parse.ActionNode:
		w.pipe(n.Pipe, rootDot)
	case *parse.IfNode:
		w.pipe(n.Pipe, rootDot)
		w.node(n.List, rootDot)
		w.node(n.ElseList, rootDot)
	case *parse.RangeNode:
		w.pipe(n.Pipe, rootDot)
		w.node(n.List, false) // im Schleifenkörper ist "." das Element
		w.node(n.ElseList, rootDot)
	case *parse.WithNode:
		w.pipe(n.Pipe, rootDot)
		w.node(n.List, false) // im with-Körper ist "." der neue Wert
		w.node(n.ElseList, rootDot)
	case *parse.TemplateNode:
		w.pipe(n.Pipe, rootDot)
	}
}

func (w *walker) pipe(p *parse.PipeNode, rootDot bool) {
	if p == nil {
		return
	}
	for _, cmd := range p.Cmds {
		for _, arg := range cmd.Args {
			w.arg(arg, rootDot)
		}
	}
}

func (w *walker) arg(a parse.Node, rootDot bool) {
	switch a := a.(type) {
	case *parse.FieldNode:
		if rootDot && len(a.Ident) > 0 {
			w.record(a.Ident[0], a)
		}
	case *parse.VariableNode:
		// $.name ist immer die Wurzel, auch in range/with
		if len(a.Ident) > 1 && a.Ident[0] == "$" {
			w.record(a.Ident[1], a)
		}
	case *parse.PipeNode:
		w.pipe(a, rootDot)
	case *parse.ChainNode:
		w.arg(a.Node, rootDot)
	}
}

// suggest liefert den ähnlichsten deklarierten Namen (Tippfehler-Hilfe) oder "".
func suggest(name string, declared map[string]bool) string {
	best, bestDist := "", 3 // maximal Abstand 2
	for _, cand := range sortedKeys(declared) {
		if d := levenshtein(name, cand); d < bestDist {
			best, bestDist = cand, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
