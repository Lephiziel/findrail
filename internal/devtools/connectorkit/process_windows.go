package connectorkit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	createSuspended        = 0x00000004
	jobObjectExtendedLimit = 9
	jobKillOnClose         = 0x00002000
	processSetQuota        = 0x0100
	processTerminate       = 0x0001
	threadSuspendResume    = 0x0002
	threadSnapshot         = 0x00000004
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	terminateJobObject       = kernel32.NewProc("TerminateJobObject")
	openProcess              = kernel32.NewProc("OpenProcess")
	createToolhelpSnapshot   = kernel32.NewProc("CreateToolhelp32Snapshot")
	thread32First            = kernel32.NewProc("Thread32First")
	thread32Next             = kernel32.NewProc("Thread32Next")
	openThread               = kernel32.NewProc("OpenThread")
	resumeThread             = kernel32.NewProc("ResumeThread")
)

type winIOCounters struct{ ReadOperationCount, WriteOperationCount, OtherOperationCount, ReadTransferCount, WriteTransferCount, OtherTransferCount uint64 }
type winBasicLimitInformation struct {
	PerProcessUserTimeLimit, PerJobUserTimeLimit int64
	LimitFlags                                   uint32
	MinimumWorkingSetSize, MaximumWorkingSetSize uintptr
	ActiveProcessLimit                           uint32
	Affinity                                     uintptr
	PriorityClass, SchedulingClass               uint32
}
type winExtendedLimitInformation struct {
	Basic                                                                        winBasicLimitInformation
	IO                                                                           winIOCounters
	ProcessMemoryLimit, JobMemoryLimit, PeakProcessMemoryUsed, PeakJobMemoryUsed uintptr
}
type winThreadEntry32 struct {
	Size, Usage, ThreadID, OwnerProcessID uint32
	BasePriority, DeltaPriority           int32
	Flags                                 uint32
}

type windowsProcessTree struct{ job syscall.Handle }

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createSuspended}
}

func attachProcessTree(cmd *exec.Cmd, publish func(processTree)) (processTree, error) {
	jobRaw, _, callErr := createJobObjectW.Call(0, 0)
	if jobRaw == 0 {
		return nil, winCallError("CreateJobObjectW", callErr)
	}
	tree := &windowsProcessTree{job: syscall.Handle(jobRaw)}
	ready := false
	defer func() {
		if !ready {
			tree.cleanup()
		}
	}()
	limits := winExtendedLimitInformation{Basic: winBasicLimitInformation{LimitFlags: jobKillOnClose}}
	if ok, _, e := setInformationJobObject.Call(jobRaw, jobObjectExtendedLimit, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		return nil, winCallError("SetInformationJobObject", e)
	}
	procRaw, _, e := openProcess.Call(processSetQuota|processTerminate, 0, uintptr(uint32(cmd.Process.Pid)))
	if procRaw == 0 {
		return nil, winCallError("OpenProcess", e)
	}
	processHandle := syscall.Handle(procRaw)
	defer syscall.CloseHandle(processHandle)
	if ok, _, e := assignProcessToJobObject.Call(jobRaw, procRaw); ok == 0 {
		return nil, winCallError("AssignProcessToJobObject", e)
	}
	thread, err := primaryThread(uint32(cmd.Process.Pid))
	if err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(thread)
	publish(tree)
	if err := resumeOwnedThread(thread); err != nil {
		return nil, err
	}
	ready = true
	return tree, nil
}

func resumeOwnedThread(thread syscall.Handle) error {
	// ResumeThread returns a DWORD, including its failure sentinel on 64-bit Windows.
	if previous, _, e := resumeThread.Call(uintptr(thread)); uint32(previous) == ^uint32(0) {
		return winCallError("ResumeThread", e)
	}
	return nil
}

func primaryThread(pid uint32) (syscall.Handle, error) {
	snapshot, _, e := createToolhelpSnapshot.Call(threadSnapshot, 0)
	if syscall.Handle(snapshot) == syscall.InvalidHandle {
		return 0, winCallError("CreateToolhelp32Snapshot", e)
	}
	defer syscall.CloseHandle(syscall.Handle(snapshot))
	entry := winThreadEntry32{Size: uint32(unsafe.Sizeof(winThreadEntry32{}))}
	ok, _, e := thread32First.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if entry.OwnerProcessID == pid {
			h, _, openErr := openThread.Call(threadSuspendResume, 0, uintptr(entry.ThreadID))
			if h == 0 {
				return 0, winCallError("OpenThread", openErr)
			}
			return syscall.Handle(h), nil
		}
		ok, _, e = thread32Next.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	return 0, fmt.Errorf("primary thread for process %d not found: %w", pid, winCallError("Thread32Next", e))
}

func (p *windowsProcessTree) stop() error {
	if ok, _, e := terminateJobObject.Call(uintptr(p.job), 1); ok == 0 {
		if errors.Is(winCallError("TerminateJobObject", e), syscall.ERROR_ACCESS_DENIED) {
			return os.ErrProcessDone
		}
		return winCallError("TerminateJobObject", e)
	}
	return nil
}

func (p *windowsProcessTree) cleanup() {
	_ = p.stop()
	_ = syscall.CloseHandle(p.job)
}

func winCallError(name string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		err = syscall.GetLastError()
	}
	return fmt.Errorf("%s: %w", name, err)
}
