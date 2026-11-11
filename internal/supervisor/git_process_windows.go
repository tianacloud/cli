//go:build windows

package supervisor

import (
	"errors"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type gitTree struct {
	command *exec.Cmd
	job     windows.Handle
}

func startGitTree(command *exec.Cmd) (*gitTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	tree := &gitTree{command: command, job: job}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		tree.close()
		return nil, err
	}
	// Suspend before assignment so Git cannot create unowned helpers.
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err = command.Start(); err != nil {
		tree.close()
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if err == nil {
		err = resumeGitThread(uint32(command.Process.Pid))
	}
	if err != nil {
		_ = command.Process.Kill()
		tree.close()
		_ = command.Wait()
		return nil, err
	}
	return tree, nil
}

func resumeGitThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		defer windows.CloseHandle(thread)
		_, err = windows.ResumeThread(thread)
		return err
	}
	return errors.New("cannot resume Git process")
}
func (t *gitTree) interrupt() {
	_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(t.command.Process.Pid))
}
func (t *gitTree) close() {
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}
