package arm64

// This file implements the interfaces required for register allocations. See regalloc/api.go.

import (
	"github.com/tetratelabs/wazero/internal/engine/wazevo/backend/regalloc"
	"github.com/tetratelabs/wazero/internal/engine/wazevo/ssa"
)

type (
	// regAllocFunctionImpl implements regalloc.Function.
	regAllocFunctionImpl struct {
		m *machine
		// iter is the iterator for reversePostOrderBlocks.
		iter                   int
		reversePostOrderBlocks []regAllocBlockImpl
		// labelToRegAllocBlockIndex maps label to the index of reversePostOrderBlocks.
		labelToRegAllocBlockIndex map[label]int
		// predsSlice is used for regalloc.Block Preds() method, defined here for reuse.
		predsSlice []regalloc.Block
		// vs is used for regalloc.Instr Defs() and Uses() methods, defined here for reuse.
		vs []regalloc.VReg
	}

	// regAllocBlockImpl implements regalloc.Block.
	regAllocBlockImpl struct {
		// f is the function this instruction belongs to. Used to reuse the regAllocFunctionImpl.predsSlice slice for Defs() and Uses().
		f   *regAllocFunctionImpl
		sb  ssa.BasicBlock
		l   label
		pos *labelPosition
		// instrImpl is re-used for all instructions in this block.
		instrImpl regAllocInstrImpl
	}

	// regAllocInstrImpl implements regalloc.Instr.
	regAllocInstrImpl struct {
		// f is the function this instruction belongs to. Used to reuse the regAllocFunctionImpl.vs slice for Defs() and Uses().
		f *regAllocFunctionImpl
		i *instruction
	}
)

func (f *regAllocFunctionImpl) addBlock(sb ssa.BasicBlock, l label, pos *labelPosition) {
	i := len(f.reversePostOrderBlocks)
	f.reversePostOrderBlocks = append(f.reversePostOrderBlocks, regAllocBlockImpl{
		f:         f,
		sb:        sb,
		l:         l,
		pos:       pos,
		instrImpl: regAllocInstrImpl{f: f},
	})
	f.labelToRegAllocBlockIndex[l] = i
}

func (f *regAllocFunctionImpl) reset() {
	f.reversePostOrderBlocks = f.reversePostOrderBlocks[:0]
	f.predsSlice = f.predsSlice[:0]
	f.vs = f.vs[:0]
	f.iter = 0
}

var (
	_ regalloc.Function = (*regAllocFunctionImpl)(nil)
	_ regalloc.Block    = (*regAllocBlockImpl)(nil)
	_ regalloc.Instr    = (*regAllocInstrImpl)(nil)
)

// PostOrderBlockIteratorBegin implements regalloc.Function PostOrderBlockIteratorBegin.
func (f *regAllocFunctionImpl) PostOrderBlockIteratorBegin() regalloc.Block {
	f.iter = len(f.reversePostOrderBlocks) - 1
	return f.PostOrderBlockIteratorNext()
}

// PostOrderBlockIteratorNext implements regalloc.Function PostOrderBlockIteratorNext.
func (f *regAllocFunctionImpl) PostOrderBlockIteratorNext() regalloc.Block {
	if f.iter < 0 {
		return nil
	}
	b := &f.reversePostOrderBlocks[f.iter]
	f.iter--
	return b
}

// ReversePostOrderBlockIteratorBegin implements regalloc.Function ReversePostOrderBlockIteratorBegin.
func (f *regAllocFunctionImpl) ReversePostOrderBlockIteratorBegin() regalloc.Block {
	f.iter = 0
	return f.ReversePostOrderBlockIteratorNext()
}

// ReversePostOrderBlockIteratorNext implements regalloc.Function ReversePostOrderBlockIteratorNext.
func (f *regAllocFunctionImpl) ReversePostOrderBlockIteratorNext() regalloc.Block {
	if f.iter >= len(f.reversePostOrderBlocks) {
		return nil
	}
	b := &f.reversePostOrderBlocks[f.iter]
	f.iter++
	return b
}

// ClobberedRegisters implements regalloc.Function ClobberedRegisters.
func (f *regAllocFunctionImpl) ClobberedRegisters(regs []regalloc.VReg) {
	m := f.m
	m.clobberedRegs = append(m.clobberedRegs[:0], regs...)
}

// StoreRegisterBefore implements regalloc.Function StoreRegisterBefore.
func (f *regAllocFunctionImpl) StoreRegisterBefore(v regalloc.VReg, _instr regalloc.Instr) {
	if !v.IsRealReg() {
		panic("BUG: VReg must be backed by real reg to be stored")
	}

	m := f.m
	typ := m.compiler.TypeOf(v)

	offsetFromSP := m.getVRegSpillSlotOffset(v.ID(), typ.Size()) + m.clobberedRegSlotSize()
	admode := m.resolveAddressModeForOffset(offsetFromSP, typ.Bits(), spVReg)
	store := m.allocateInstrAfterLowering()
	store.asStore(operandNR(v), admode, typ.Bits())

	instr := _instr.(*regAllocInstrImpl).i
	prev := instr.prev

	prev.next = store
	store.prev = prev

	store.next = instr
	instr.prev = store
}

// ReloadRegisterBefore implements regalloc.Function ReloadRegisterBefore.
func (f *regAllocFunctionImpl) ReloadRegisterBefore(v regalloc.VReg, instr regalloc.Instr) {
	panic("TODO")
}

// ReloadRegisterAfter implements regalloc.Function ReloadRegisterAfter.
func (f *regAllocFunctionImpl) ReloadRegisterAfter(v regalloc.VReg, _instr regalloc.Instr) {
	if !v.IsRealReg() {
		panic("BUG: VReg must be backed by real reg to be stored")
	}

	m := f.m
	typ := m.compiler.TypeOf(v)

	offsetFromSP := m.getVRegSpillSlotOffset(v.ID(), typ.Size()) + m.clobberedRegSlotSize()
	admode := m.resolveAddressModeForOffset(offsetFromSP, typ.Bits(), spVReg)
	load := m.allocateInstrAfterLowering()
	switch typ {
	case ssa.TypeI32, ssa.TypeI64:
		load.asULoad(operandNR(v), admode, typ.Bits())
	case ssa.TypeF32, ssa.TypeF64:
		load.asFpuLoad(operandNR(v), admode, typ.Bits())
	default:
		panic("TODO")
	}

	instr := _instr.(*regAllocInstrImpl).i
	next := instr.next

	instr.next = load
	load.prev = instr

	load.next = next
	next.prev = load
}

// StoreRegisterAfter implements regalloc.Function StoreRegisterAfter.
func (f *regAllocFunctionImpl) StoreRegisterAfter(v regalloc.VReg, _instr regalloc.Instr) {
	panic("TODO")
}

// Done implements regalloc.Function Done.
func (f *regAllocFunctionImpl) Done() {
	m := f.m
	// Now that we know the final spill slot size, we must align spillSlotSize to 16 bytes.
	m.spillSlotSize = (m.spillSlotSize + 15) &^ 15
}

// ID implements regalloc.Block ID.
func (r *regAllocBlockImpl) ID() int {
	return int(r.l)
}

// Preds implements regalloc.Block Preds.
func (r *regAllocBlockImpl) Preds() []regalloc.Block {
	sb := r.sb
	r.f.predsSlice = r.f.predsSlice[:0]
	for pred := sb.NextPredIterator(); pred != nil; pred = sb.NextPredIterator() {
		l := r.f.m.ssaBlockIDToLabels[pred.ID()]
		index := r.f.labelToRegAllocBlockIndex[l]
		r.f.predsSlice = append(r.f.predsSlice, &r.f.reversePostOrderBlocks[index])
	}
	return r.f.predsSlice
}

// InstrIteratorBegin implements regalloc.Block InstrIteratorBegin.
func (r *regAllocBlockImpl) InstrIteratorBegin() regalloc.Instr {
	r.instrImpl.i = r.pos.begin
	return &r.instrImpl
}

// InstrIteratorNext implements regalloc.Block InstrIteratorNext.
func (r *regAllocBlockImpl) InstrIteratorNext() regalloc.Instr {
	for {
		instr := r.instrIteratorNext()
		if instr == nil {
			return nil
		} else if !instr.i.addedAfterLowering {
			// Skips the instruction added after lowering.
			return instr
		}
	}
}

func (r *regAllocBlockImpl) instrIteratorNext() *regAllocInstrImpl {
	cur := r.instrImpl.i
	if r.pos.end == cur {
		return nil
	}
	r.instrImpl.i = cur.next
	return &r.instrImpl
}

// Entry implements regalloc.Block Entry.
func (r *regAllocBlockImpl) Entry() bool { return r.sb.EntryBlock() }

// Defs implements regalloc.Instr Defs.
func (r *regAllocInstrImpl) Defs() []regalloc.VReg {
	regs := r.f.vs[:0]
	regs = r.i.defs(regs)
	r.f.vs = regs
	return regs
}

// Uses implements regalloc.Instr Uses.
func (r *regAllocInstrImpl) Uses() []regalloc.VReg {
	regs := r.f.vs[:0]
	regs = r.i.uses(regs)
	r.f.vs = regs
	return regs
}

// IsCopy implements regalloc.Instr IsCopy.
func (r *regAllocInstrImpl) IsCopy() bool {
	return r.i.isCopy()
}

// RegisterInfo implements backend.Machine.
func (m *machine) RegisterInfo() *regalloc.RegisterInfo {
	return regInfo
}

// Function implements backend.Machine Function.
func (m *machine) Function() regalloc.Function {
	return &m.regAllocFn
}

// IsCall implements regalloc.Instr IsCall.
func (r *regAllocInstrImpl) IsCall() bool {
	return r.i.kind == call
}

// IsIndirectCall implements regalloc.Instr IsIndirectCall.
func (r *regAllocInstrImpl) IsIndirectCall() bool {
	return r.i.kind == callInd
}

// IsReturn implements regalloc.Instr IsReturn.
func (r *regAllocInstrImpl) IsReturn() bool {
	return r.i.kind == ret
}

// AssignUses implements regalloc.Instr AssignUses.
func (r *regAllocInstrImpl) AssignUses(vs []regalloc.VReg) {
	r.i.assignUses(vs)
}

// AssignDef implements regalloc.Instr AssignDef.
func (r *regAllocInstrImpl) AssignDef(v regalloc.VReg) {
	r.i.assignDef(v)
}
