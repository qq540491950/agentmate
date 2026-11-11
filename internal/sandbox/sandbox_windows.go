//go:build windows

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	appContainerName = "AgentMate.Sandbox"
	appContainerDesc = "AgentMate local tool sandbox"
	hrAlreadyExists  = 0x800700B7
	createNoWindow   = 0x08000000
)

var (
	userenvDLL                  = windows.NewLazySystemDLL("userenv.dll")
	advapiDLL                   = windows.NewLazySystemDLL("advapi32.dll")
	kernelbaseDLL               = windows.NewLazySystemDLL("kernelbase.dll")
	createProfileProc           = userenvDLL.NewProc("CreateAppContainerProfile")
	deriveSIDProc               = userenvDLL.NewProc("DeriveAppContainerSidFromAppContainerName")
	createAppContainerTokenProc = kernelbaseDLL.NewProc("CreateAppContainerToken")
	initializeACLProc           = advapiDLL.NewProc("InitializeAcl")
	addMandatoryAceProc         = advapiDLL.NewProc("AddMandatoryAce")
)

type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

type windowsBackend struct {
	mu        sync.Mutex
	sid       *windows.SID
	sidString string
	acls      map[string]bool
	labeled   map[string]bool
}

var (
	backendOnce sync.Once
	backend     *windowsBackend
	backendErr  error
)

func getBackend() (*windowsBackend, error) {
	backendOnce.Do(func() {
		sid, err := ensureAppContainerProfile()
		if err != nil {
			backendErr = err
			return
		}
		backend = &windowsBackend{
			sid: sid, sidString: sid.String(),
			acls: map[string]bool{}, labeled: map[string]bool{},
		}
	})
	return backend, backendErr
}

func ensureAppContainerProfile() (*windows.SID, error) {
	name, err := windows.UTF16PtrFromString(appContainerName)
	if err != nil {
		return nil, err
	}
	display, err := windows.UTF16PtrFromString("AgentMate Sandbox")
	if err != nil {
		return nil, err
	}
	desc, err := windows.UTF16PtrFromString(appContainerDesc)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	hr, _, _ := createProfileProc.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(display)),
		uintptr(unsafe.Pointer(desc)),
		0, 0,
		uintptr(unsafe.Pointer(&sid)),
	)
	if uint32(hr) == 0 {
		return sid, nil
	}
	if uint32(hr) != hrAlreadyExists {
		return nil, fmt.Errorf("CreateAppContainerProfile 失败: HRESULT 0x%08X", uint32(hr))
	}
	hr, _, _ = deriveSIDProc.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if uint32(hr) != 0 {
		return nil, fmt.Errorf("DeriveAppContainerSidFromAppContainerName 失败: HRESULT 0x%08X", uint32(hr))
	}
	return sid, nil
}

func runPlatform(ctx context.Context, req Request, policy Policy) (Result, error) {
	b, err := getBackend()
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	home, err := b.prepare(policy)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer os.RemoveAll(home)
	return b.run(ctx, req, policy, home)
}

func (b *windowsBackend) prepare(policy Policy) (string, error) {
	home, err := os.MkdirTemp("", "agentmate-sandbox-*")
	if err != nil {
		return "", fmt.Errorf("创建沙箱目录失败: %w", err)
	}
	for _, dir := range []string{
		home,
		filepath.Join(home, "AppData", "Roaming"),
		filepath.Join(home, "AppData", "Local"),
		filepath.Join(home, "Temp"),
		filepath.Join(home, ".config"),
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".local", "share"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			_ = os.RemoveAll(home)
			return "", fmt.Errorf("创建沙箱目录失败: %w", err)
		}
	}
	if err := b.grantModify(home); err != nil {
		_ = os.RemoveAll(home)
		return "", err
	}
	if err := b.labelLowIntegrity(home, nil); err != nil {
		_ = os.RemoveAll(home)
		return "", err
	}
	roots := append([]string{policy.Workspace}, policy.WritableRoots...)
	for _, root := range roots {
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			continue
		}
		if err := b.grantModify(root); err != nil {
			_ = os.RemoveAll(home)
			return "", err
		}
		for _, path := range policy.ProtectedPaths {
			if !withinPath(root, path) {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				continue
			}
			mediumSID, sidErr := windows.StringToSid("S-1-16-8192")
			if sidErr != nil {
				_ = os.RemoveAll(home)
				return "", sidErr
			}
			info, statErr := os.Stat(path)
			if statErr != nil {
				continue
			}
			if err := setIntegrityLabel(path, info.IsDir(), mediumSID, 1); err != nil {
				_ = os.RemoveAll(home)
				return "", err
			}
		}
		if strings.EqualFold(filepath.Clean(root), filepath.Clean(policy.Workspace)) {
			if err := b.labelLowIntegrity(root, policy.ProtectedPaths); err != nil {
				_ = os.RemoveAll(home)
				return "", err
			}
		} else {
			lowSID, sidErr := windows.StringToSid("S-1-16-4096")
			if sidErr != nil {
				_ = os.RemoveAll(home)
				return "", sidErr
			}
			if err := setLowIntegrityLabel(root, true, lowSID); err != nil {
				_ = os.RemoveAll(home)
				return "", err
			}
		}
	}
	inWritableRoot := func(path string) bool {
		for _, root := range roots {
			if withinPath(root, path) {
				return true
			}
		}
		return false
	}
	for _, path := range policy.ProtectedPaths {
		// A protected path outside every writable root already has no
		// AppContainer access; adding a deny ACE would only slow startup.
		if !inWritableRoot(path) {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if isWriteProtectedOnly(path) {
			if err := b.denyWrite(path); err != nil {
				_ = os.RemoveAll(home)
				return "", err
			}
		} else if err := b.denyFull(path); err != nil {
			_ = os.RemoveAll(home)
			return "", err
		}
	}
	return home, nil
}

func isWriteProtectedOnly(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case ".git", ".agents", ".codex":
		return true
	default:
		return false
	}
}

func (b *windowsBackend) run(ctx context.Context, req Request, policy Policy, home string) (Result, error) {
	env := sanitizedEnvironment(home, append(policy.ExtraEnv, req.Env...))
	envBlock, err := buildEnvBlock(env)
	if err != nil {
		return Result{}, err
	}
	command := req.Command
	if resolved, lookErr := exec.LookPath(command); lookErr == nil {
		command = resolved
	}
	appName, err := windows.UTF16PtrFromString(command)
	if err != nil {
		return Result{}, err
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{command}, req.Args...)))
	if err != nil {
		return Result{}, err
	}
	cwd, err := windows.UTF16PtrFromString(req.Dir)
	if err != nil {
		return Result{}, err
	}

	nul, err := os.OpenFile("NUL", os.O_RDWR, 0)
	if err != nil {
		return Result{}, err
	}
	defer nul.Close()
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		return Result{}, err
	}
	defer outRead.Close()
	defer outWrite.Close()

	inHandle := windows.Handle(nul.Fd())
	outHandle := windows.Handle(outWrite.Fd())
	handles := []windows.Handle{inHandle, outHandle}
	for _, h := range handles {
		if err := windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return Result{}, err
		}
	}

	attributeList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return Result{}, fmt.Errorf("创建进程属性失败: %w", err)
	}
	defer attributeList.Delete()
	if err := attributeList.Update(
		windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&handles[0]),
		uintptr(len(handles))*unsafe.Sizeof(handles[0]),
	); err != nil {
		return Result{}, fmt.Errorf("设置句柄继承失败: %w", err)
	}
	si := &windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:    uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags: windows.STARTF_USESTDHANDLES,
		},
		ProcThreadAttributeList: attributeList.List(),
	}
	si.StdInput = inHandle
	si.StdOutput = outHandle
	si.StdErr = outHandle

	token, err := b.appContainerToken(policy.Network)
	if err != nil {
		return Result{}, err
	}
	defer token.Close()

	pi := new(windows.ProcessInformation)
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED | createNoWindow)
	if err := windows.CreateProcessAsUser(
		token, appName, cmdLine, nil, nil, true, flags, envBlock, cwd,
		&si.StartupInfo, pi,
	); err != nil {
		return Result{}, fmt.Errorf("启动沙箱进程失败: %w", err)
	}
	runtime.KeepAlive(handles)
	runtime.KeepAlive(envBlock)
	defer windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return Result{}, fmt.Errorf("创建进程任务对象失败: %w", err)
	}
	jobOpen := true
	defer func() {
		if jobOpen {
			_ = windows.CloseHandle(job)
		}
	}()
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return Result{}, fmt.Errorf("配置进程任务对象失败: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return Result{}, fmt.Errorf("将进程加入任务对象失败: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		return Result{}, fmt.Errorf("恢复沙箱进程失败: %w", err)
	}

	// The child owns the write end; the parent must close its copy or the read
	// loop never observes EOF after the child exits.
	if err := outWrite.Close(); err != nil {
		return Result{}, err
	}

	out := &limitedBuffer{limit: req.OutputLimit}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, outRead)
		close(copyDone)
	}()

	waitCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	timedOut := false
	for {
		event, err := windows.WaitForSingleObject(pi.Process, 50)
		if err != nil {
			return Result{}, err
		}
		if event == windows.WAIT_OBJECT_0 {
			break
		}
		if waitCtx.Err() == nil {
			continue
		}
		timedOut = errors.Is(waitCtx.Err(), context.DeadlineExceeded)
		_ = windows.TerminateJobObject(job, 1)
		_, _ = windows.WaitForSingleObject(pi.Process, 5000)
		break
	}
	// Closing the job also kills descendants that inherited the job.
	_ = windows.CloseHandle(job)
	jobOpen = false
	_ = outRead.Close()
	select {
	case <-copyDone:
	case <-time.After(2 * time.Second):
	}

	if ctx.Err() != nil && !timedOut {
		return Result{}, fmt.Errorf("命令已取消: %w", ctx.Err())
	}
	var code uint32
	if err := windows.GetExitCodeProcess(pi.Process, &code); err != nil {
		return Result{}, err
	}
	res := Result{Output: out.String(), Truncated: out.Truncated()}
	if timedOut {
		res.TimedOut = true
		res.ExitCode = -1
		return res, nil
	}
	res.ExitCode = int(code)
	return res, nil
}

func (b *windowsBackend) appContainerToken(network Network) (windows.Token, error) {
	var current windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_ALL_ACCESS,
		&current,
	); err != nil {
		return 0, fmt.Errorf("打开当前进程令牌失败: %w", err)
	}
	defer current.Close()
	caps, capSIDs, err := b.capabilities(network)
	if err != nil {
		return 0, err
	}
	var token windows.Token
	ok, _, callErr := createAppContainerTokenProc.Call(
		uintptr(current),
		uintptr(unsafe.Pointer(&caps)),
		uintptr(unsafe.Pointer(&token)),
	)
	if ok == 0 {
		return 0, fmt.Errorf("创建 AppContainer 令牌失败: %v", callErr)
	}
	runtime.KeepAlive(capSIDs)
	return token, nil
}

func (b *windowsBackend) capabilities(network Network) (securityCapabilities, []*windows.SID, error) {
	caps := securityCapabilities{AppContainerSid: b.sid}
	if network != NetworkOn {
		return caps, nil, nil
	}
	sid, err := windows.StringToSid("S-1-15-3-1") // internetClient
	if err != nil {
		return caps, nil, err
	}
	items := []windows.SIDAndAttributes{{Sid: sid, Attributes: windows.SE_GROUP_ENABLED}}
	caps.Capabilities = &items[0]
	caps.CapabilityCount = 1
	return caps, []*windows.SID{sid}, nil
}

func (b *windowsBackend) labelLowIntegrity(root string, protected []string) error {
	key := strings.ToLower(filepath.Clean(root)) + "\x00" + strings.Join(protected, "\x00")
	b.mu.Lock()
	done := b.labeled[key]
	b.mu.Unlock()
	if done {
		return nil
	}
	lowSID, err := windows.StringToSid("S-1-16-4096")
	if err != nil {
		return err
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path != root && isProtectedPath(path, protected) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return setLowIntegrityLabel(path, entry.IsDir(), lowSID)
	})
	if err == nil {
		b.mu.Lock()
		b.labeled[key] = true
		b.mu.Unlock()
	}
	return err
}

func isProtectedPath(path string, protected []string) bool {
	for _, item := range protected {
		if withinPath(item, path) {
			return true
		}
	}
	return false
}

func setLowIntegrityLabel(path string, isDir bool, lowSID *windows.SID) error {
	return setIntegrityLabel(path, isDir, lowSID, 1)
}

func setIntegrityLabel(path string, isDir bool, sid *windows.SID, policy uint32) error {
	buf := make([]byte, 256)
	acl := (*windows.ACL)(unsafe.Pointer(&buf[0]))
	ok, _, callErr := initializeACLProc.Call(
		uintptr(unsafe.Pointer(acl)),
		uintptr(len(buf)),
		2,
	)
	if ok == 0 {
		return fmt.Errorf("InitializeAcl(%s): %v", path, callErr)
	}
	var flags uint32
	if isDir {
		flags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	ok, _, callErr = addMandatoryAceProc.Call(
		uintptr(unsafe.Pointer(acl)),
		2,
		uintptr(flags),
		uintptr(policy),
		uintptr(unsafe.Pointer(sid)),
	)
	if ok == 0 {
		return fmt.Errorf("AddMandatoryAce(%s): %v", path, callErr)
	}
	err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION,
		nil, nil, nil, acl,
	)
	runtime.KeepAlive(buf)
	if err != nil {
		return fmt.Errorf("设置完整性标签失败 %s: %w", path, err)
	}
	return nil
}

func buildEnvBlock(env []string) (*uint16, error) {
	sort.Strings(env)
	var block []uint16
	for _, entry := range env {
		u, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, u...)
	}
	block = append(block, 0)
	return &block[0], nil
}

func (b *windowsBackend) grantModify(path string) error {
	return b.icacls(path, "/grant", "*"+b.sidString+":(OI)(CI)M")
}

func (b *windowsBackend) denyWrite(path string) error {
	return b.icacls(path, "/deny", "*"+b.sidString+":(OI)(CI)(WD,AD,WEA,WA,DC,DE)")
}

func (b *windowsBackend) denyFull(path string) error {
	return b.icacls(path, "/deny", "*"+b.sidString+":(OI)(CI)F")
}

func (b *windowsBackend) removeACL(path string) error {
	return b.icacls(path, "/remove", "*"+b.sidString)
}

func (b *windowsBackend) icacls(path, verb, rights string) error {
	key := strings.ToLower(filepath.Clean(path)) + "\x00" + verb + "\x00" + rights
	b.mu.Lock()
	if b.acls[key] {
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()

	cmd := exec.Command("icacls", path, verb, rights, "/C", "/Q")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls %s %s 失败: %w: %s", path, verb, err, strings.TrimSpace(string(out)))
	}

	b.mu.Lock()
	b.acls[key] = true
	b.mu.Unlock()
	return nil
}

func statusPlatform() Status {
	b, err := getBackend()
	if err != nil {
		return Status{Available: false, Backend: "windows-appcontainer", Error: err.Error()}
	}
	return Status{Available: true, Backend: "windows-appcontainer", ProfileSID: b.sidString}
}

func closePlatform() {
	b, err := getBackend()
	if err != nil || b == nil {
		return
	}
	b.mu.Lock()
	paths := make([]string, 0, len(b.acls))
	for key := range b.acls {
		parts := strings.Split(key, "\x00")
		if len(parts) > 0 && parts[0] != "" {
			paths = append(paths, parts[0])
		}
	}
	b.mu.Unlock()
	for _, path := range paths {
		_ = b.removeACL(path)
	}
}
