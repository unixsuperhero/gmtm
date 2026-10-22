// Package modal provides a stack of modal layers rendered inside the frame.
//
// Each Layer has a Title and an opaque Body component (anything with
// Update(tea.Msg) (Component, tea.Cmd) and View() string); the layer keeps its
// own state so popping restores the parent exactly.
//
//	var s modal.Stack
//	s = s.Push(modal.Layer{Title: "Group", Body: myComponent})
//	s, cmd = s.Update(msg)          // forwarded to the top layer only
//	s.Breadcrumb()                  // "Palette › New › Terminal"
//	out := s.Render(base, w, h)     // top layer boxed and centred over base
//	s, top, ok := s.Pop()
//
// Stack is a value type: Push/Pop/Replace return a new Stack and never mutate
// the receiver's backing array. Escape handling is the body's job (palette,
// jump/context and note have different rules); bodies report closing through
// their own messages and the orchestrator pops.
//
// Wrap adapts a component whose Update returns its own concrete type.
package modal
