package audio

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/store"
)

const (
	ModelName  = "Confucius4-R2T2-Q8_0.gguf"
	MMProjName = "mmproj-Confucius4-R2T2-Q8_0.gguf"
	EnabledKey = "plugin:r2t2:enabled"
	ModelBase  = "https://huggingface.co/netease-youdao/Confucius4-R2T2-GGUF/resolve/main/"
	RuntimeURL = "https://github.com/ggml-org/llama.cpp/releases/download/b10950/llama-b10950-bin-win-vulkan-x64.zip"
)

type LocalR2T2Manager struct {
	store          *store.Store
	resourceDir    string
	runtimeDir     string
	modelDir       string
	serverPath     string
	modelPath      string
	mmprojPath     string
	httpClient     *http.Client
	processFactory ProcessFactory
	appCtx         context.Context

	mu            sync.RWMutex
	inferenceMu   sync.Mutex
	cancel        context.CancelFunc
	workerRunning bool
	workerWg      sync.WaitGroup
	cmd           *exec.Cmd
	processExit   chan struct{}
	port          int
	state         string // disabled, starting, installing, running, stopping, error
	errText       string
	progress      *float64
	enabled       bool
}

func newLocalR2T2Manager(opts Options, appCtx context.Context) (*LocalR2T2Manager, error) {
	resourceDir := opts.ResourceDir
	if resourceDir == "" {
		if env := os.Getenv("ASTRORDER_R2T2_DIR"); env != "" {
			resourceDir = env
		} else {
			local := os.Getenv("LOCALAPPDATA")
			if local == "" {
				home, _ := os.UserHomeDir()
				local = filepath.Join(home, "AppData", "Local")
			}
			resourceDir = filepath.Join(local, "Astrorder", "plugins", "r2t2")
		}
	}

	runtimeDir := filepath.Join(resourceDir, "runtime")
	modelDir := filepath.Join(resourceDir, "models")
	exeName := "llama-server"
	if runtime.GOOS == "windows" {
		exeName = "llama-server.exe"
	}
	serverPath := filepath.Join(runtimeDir, exeName)
	modelPath := filepath.Join(modelDir, ModelName)
	mmprojPath := filepath.Join(modelDir, MMProjName)

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	m := &LocalR2T2Manager{
		store:          opts.Store,
		resourceDir:    resourceDir,
		runtimeDir:     runtimeDir,
		modelDir:       modelDir,
		serverPath:     serverPath,
		modelPath:      modelPath,
		mmprojPath:     mmprojPath,
		httpClient:     client,
		processFactory: opts.ProcessFactory,
		appCtx:         appCtx,
		state:          "disabled",
	}

	enabled, err := m.readEnabled(appCtx)
	if err != nil {
		// Log or record read error
		m.errText = fmt.Sprintf("读取插件状态失败: %v", err)
	}
	m.enabled = enabled
	return m, nil
}

func (m *LocalR2T2Manager) readEnabled(ctx context.Context) (bool, error) {
	if m.store == nil {
		return false, nil
	}
	pref, err := m.store.GetPreference(ctx, EnabledKey)
	if err != nil {
		return false, err
	}
	if !pref.Exists || pref.Value == nil {
		return false, nil
	}
	if b, ok := pref.Value.(bool); ok {
		return b, nil
	}
	return false, nil
}

func (m *LocalR2T2Manager) persistEnabled(ctx context.Context, val bool) error {
	if m.store == nil {
		return nil
	}
	return m.store.SetPreference(ctx, EnabledKey, val, true)
}

func (m *LocalR2T2Manager) Restore(ctx context.Context) error {
	m.mu.RLock()
	en := m.enabled
	m.mu.RUnlock()
	if !en {
		return nil
	}

	// Verify resources exist. On startup, do NOT trigger implicit multi-gigabyte downloads.
	available := fileExists(m.serverPath) && fileExists(m.modelPath) && fileExists(m.mmprojPath)
	if !available {
		m.mu.Lock()
		m.state = "error"
		m.errText = "本地语音模型资源未下载，请在设置中手动启用以下载资源。"
		m.mu.Unlock()
		return errors.New(m.errText)
	}

	return m.Enable(false, false)
}

func (m *LocalR2T2Manager) Enable(wait bool, persist bool) error {
	if m.appCtx.Err() != nil {
		return errors.New("服务已关闭。")
	}

	m.mu.Lock()
	if persist {
		if err := m.persistEnabled(m.appCtx, true); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("保存配置失败: %w", err)
		}
	}
	m.enabled = true

	if m.workerRunning {
		m.mu.Unlock()
		if wait {
			return m.waitForRunning(180 * time.Second)
		}
		return nil
	}

	ctx, cancel := context.WithCancel(m.appCtx)
	m.cancel = cancel
	m.state = "starting"
	m.errText = ""
	m.workerRunning = true
	m.workerWg.Add(1)
	m.mu.Unlock()

	readyChan := make(chan error, 1)
	go m.startWorker(ctx, readyChan)

	if wait {
		select {
		case err := <-readyChan:
			return err
		case <-time.After(180 * time.Second):
			return errors.New("本地语音模型启动超时。")
		}
	}
	return nil
}

func (m *LocalR2T2Manager) waitForRunning(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		st := m.state
		errTxt := m.errText
		m.mu.RUnlock()
		if st == "running" {
			return nil
		}
		if st == "error" {
			if errTxt != "" {
				return errors.New(errTxt)
			}
			return errors.New("本地语音模型启动失败。")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("本地语音模型启动超时。")
}

func (m *LocalR2T2Manager) Disable() error {
	m.mu.Lock()
	if err := m.persistEnabled(m.appCtx, false); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("保存配置失败: %w", err)
	}
	m.enabled = false
	if m.cancel != nil {
		m.cancel()
	}
	cmd := m.cmd
	exitCh := m.processExit
	m.cmd = nil
	m.processExit = nil
	m.port = 0
	m.state = "disabled"
	m.errText = ""
	m.progress = nil
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		if exitCh != nil {
			select {
			case <-exitCh:
			case <-time.After(5 * time.Second):
			}
		}
	}

	// Wait for worker goroutine to exit
	m.workerWg.Wait()
	return nil
}

func (m *LocalR2T2Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	cmd := m.cmd
	exitCh := m.processExit
	m.cmd = nil
	m.processExit = nil
	m.port = 0
	m.state = "disabled"
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		if exitCh != nil {
			select {
			case <-exitCh:
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}

	workerDone := make(chan struct{})
	go func() {
		m.workerWg.Wait()
		close(workerDone)
	}()

	select {
	case <-workerDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *LocalR2T2Manager) IsHealthy(ctx context.Context) bool {
	m.mu.RLock()
	port := m.port
	st := m.state
	m.mu.RUnlock()

	if st != "running" || port == 0 {
		return false
	}

	checkURL := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := m.httpClient
	if client == nil {
		client = &http.Client{Timeout: 1 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *LocalR2T2Manager) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()

	available := fileExists(m.serverPath) && fileExists(m.modelPath) && fileExists(m.mmprojPath)
	var pid *int

	// Verify actual process monitor status
	if m.cmd != nil && m.cmd.Process != nil && m.processExit != nil {
		select {
		case <-m.processExit:
			// Process exited already
			if m.state == "running" {
				m.state = "error"
				m.errText = "本地语音服务意外退出。"
			}
			m.cmd = nil
			m.processExit = nil
			m.port = 0
		default:
			p := m.cmd.Process.Pid
			pid = &p
		}
	}

	result := map[string]any{
		"id":        "r2t2-local-voice",
		"name":      "R2T2 本地流式听写",
		"enabled":   m.enabled,
		"state":     m.state,
		"available": available,
		"progress":  m.progress,
		"error":     nil,
		"pid":       pid,
		"device":    "Vulkan0",
		"model":     ModelName,
	}
	if m.errText != "" {
		result["error"] = m.errText
	}
	return result
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func (m *LocalR2T2Manager) startWorker(ctx context.Context, readyChan chan<- error) {
	defer func() {
		m.mu.Lock()
		m.workerRunning = false
		m.mu.Unlock()
		m.workerWg.Done()
	}()

	err := m.ensureResources(ctx)
	if err != nil {
		m.mu.Lock()
		if !m.enabled || ctx.Err() != nil {
			m.state = "disabled"
			m.errText = ""
		} else {
			m.state = "error"
			m.errText = err.Error()
		}
		m.mu.Unlock()
		readyChan <- err
		return
	}

	select {
	case <-ctx.Done():
		readyChan <- ctx.Err()
		return
	default:
	}

	port, err := findFreePort()
	if err != nil {
		m.mu.Lock()
		m.state = "error"
		m.errText = err.Error()
		m.mu.Unlock()
		readyChan <- err
		return
	}

	_ = os.MkdirAll(m.resourceDir, 0o755)
	args := []string{
		"-m", m.modelPath,
		"--mmproj", m.mmprojPath,
		"--device", "Vulkan0",
		"--mmproj-device", "Vulkan0",
		"-ngl", "99",
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		"--parallel", "1",
		"--ctx-size", "4096",
	}

	var cmd *exec.Cmd
	if m.processFactory != nil {
		cmd, err = m.processFactory(ctx, m.serverPath, args...)
		if err != nil {
			m.mu.Lock()
			m.state = "error"
			m.errText = err.Error()
			m.mu.Unlock()
			readyChan <- err
			return
		}
	} else {
		cmd = exec.CommandContext(ctx, m.serverPath, args...)
		cmd.Dir = m.runtimeDir
		configurePlatformProcess(cmd)
	}

	logFile, _ := os.OpenFile(filepath.Join(m.resourceDir, "llama-server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if logFile != nil {
		defer logFile.Close()
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	releaseOwner, err := startPlatformProcess(cmd)
	if err != nil {
		m.mu.Lock()
		m.state = "error"
		m.errText = err.Error()
		m.mu.Unlock()
		readyChan <- err
		return
	}

	exitCh := make(chan struct{})

	m.mu.Lock()
	m.cmd = cmd
	m.processExit = exitCh
	m.port = port
	m.state = "starting"
	m.mu.Unlock()

	// Wait for process exit in background monitor (only monitor calls cmd.Wait)
	go func() {
		defer close(exitCh)
		if releaseOwner != nil {
			defer releaseOwner()
		}
		_ = cmd.Wait()

		m.mu.Lock()
		if m.state != "running" && m.state != "starting" {
			// Already disabled or stopped, don't overwrite with error
		} else if m.state == "running" {
			m.state = "error"
			m.errText = "本地语音服务意外退出。"
		}
		if m.cmd == cmd {
			m.cmd = nil
			m.port = 0
		}
		m.mu.Unlock()
	}()

	// Wait until ready
	err = m.waitUntilReady(ctx, port)
	if err != nil {
		m.mu.Lock()
		if !m.enabled || ctx.Err() != nil {
			m.state = "disabled"
			m.errText = ""
		} else {
			m.state = "error"
			m.errText = err.Error()
		}
		m.mu.Unlock()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-exitCh
		readyChan <- err
		return
	}

	m.mu.Lock()
	m.state = "running"
	m.progress = nil
	m.mu.Unlock()
	readyChan <- nil

	// Keep worker goroutine alive until process exits or context cancelled
	select {
	case <-exitCh:
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-exitCh
	}
}

func (m *LocalR2T2Manager) waitUntilReady(ctx context.Context, port int) error {
	deadline := time.Now().Add(120 * time.Second)
	checkURL := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: 1 * time.Second}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return errors.New("启动已取消。")
		default:
		}

		m.mu.RLock()
		cmd := m.cmd
		exited := m.processExit
		m.mu.RUnlock()
		if cmd == nil {
			return errors.New("llama.cpp 服务在模型加载期间退出。")
		}
		select {
		case <-exited:
			return errors.New("llama.cpp 服务在模型加载期间退出。")
		default:
		}

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("本地语音模型加载超时。")
}

func (m *LocalR2T2Manager) ensureResources(ctx context.Context) error {
	if fileExists(m.serverPath) && fileExists(m.modelPath) && fileExists(m.mmprojPath) {
		return nil
	}

	m.mu.Lock()
	m.state = "installing"
	prog := 0.0
	m.progress = &prog
	m.mu.Unlock()

	_ = os.MkdirAll(m.runtimeDir, 0o755)
	_ = os.MkdirAll(m.modelDir, 0o755)

	type dlTask struct {
		url    string
		target string
	}
	var downloads []dlTask
	runtimeZip := filepath.Join(m.resourceDir, "llama-vulkan.zip")

	if !fileExists(m.serverPath) {
		downloads = append(downloads, dlTask{url: RuntimeURL, target: runtimeZip})
	}
	if !fileExists(m.modelPath) {
		downloads = append(downloads, dlTask{url: ModelBase + ModelName, target: m.modelPath})
	}
	if !fileExists(m.mmprojPath) {
		downloads = append(downloads, dlTask{url: ModelBase + MMProjName, target: m.mmprojPath})
	}

	total := len(downloads)
	for i, t := range downloads {
		if err := m.downloadFile(ctx, t.url, t.target, i, total); err != nil {
			return err
		}
	}

	if fileExists(runtimeZip) && !fileExists(m.serverPath) {
		if err := unzip(runtimeZip, m.runtimeDir); err != nil {
			return fmt.Errorf("unzip runtime: %w", err)
		}
		_ = os.Remove(runtimeZip)
	}

	if !fileExists(m.serverPath) || !fileExists(m.modelPath) || !fileExists(m.mmprojPath) {
		return errors.New("本地语音插件资源不完整。")
	}

	return nil
}

func (m *LocalR2T2Manager) downloadFile(ctx context.Context, url, target string, index, total int) error {
	part := target + ".part"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Astrorder/0.1")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("安装已取消。")
		}
		return fmt.Errorf("download error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download status %d for %s", resp.StatusCode, url)
	}

	out, err := os.Create(part)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	contentLength := resp.ContentLength
	var received int64
	buf := make([]byte, 1024*1024)

	for {
		select {
		case <-ctx.Done():
			return errors.New("安装已取消。")
		default:
		}

		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := out.Write(buf[:n]); wErr != nil {
				return wErr
			}
			received += int64(n)
			var fraction float64
			if contentLength > 0 {
				fraction = float64(received) / float64(contentLength)
			}
			p := (float64(index) + fraction) / float64(total)
			m.mu.Lock()
			m.progress = &p
			m.mu.Unlock()
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			return rErr
		}
	}

	_ = out.Close()
	return os.Rename(part, target)
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", fpath)
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(fpath, os.ModePerm)
			continue
		}
		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}
		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func findFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// TranscribePCM converts raw 16kHz 16-bit mono PCM into WAV, calls llama.cpp server /v1/chat/completions, and removes <asr_text> prefix.
func (m *LocalR2T2Manager) TranscribePCM(ctx context.Context, pcm []byte) (string, error) {
	m.mu.RLock()
	if m.state != "running" || m.port == 0 {
		m.mu.RUnlock()
		return "", errors.New("R2T2 本地流式听写插件未运行。")
	}
	port := m.port
	m.mu.RUnlock()

	if len(pcm) == 0 {
		return "", nil
	}

	wavBytes := pcmToWav(pcm, 16000, 1, 2)
	b64Wav := base64.StdEncoding.EncodeToString(wavBytes)

	payload := map[string]any{
		"model": ModelName,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "language Chinese"},
					{"type": "input_audio", "input_audio": map[string]string{
						"data":   b64Wav,
						"format": "wav",
					}},
				},
			},
		},
		"temperature": 0,
		"max_tokens":  256,
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	targetURL := fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port)

	m.inferenceMu.Lock()
	defer m.inferenceMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := m.httpClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("inference request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("inference upstream status: %d", resp.StatusCode)
	}

	var resObj struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&resObj); err != nil {
		return "", errors.New("本地语音模型返回了无效结果。")
	}
	if len(resObj.Choices) == 0 {
		return "", errors.New("本地语音模型返回了无效文本。")
	}

	text := resObj.Choices[0].Message.Content
	marker := "<asr_text>"
	if idx := strings.Index(text, marker); idx != -1 {
		text = text[idx+len(marker):]
	}

	return strings.TrimSpace(text), nil
}

func pcmToWav(pcm []byte, sampleRate int, numChannels int, bytesPerSample int) []byte {
	buf := new(bytes.Buffer)
	dataSize := uint32(len(pcm))
	chunkSize := 36 + dataSize
	byteRate := uint32(sampleRate * numChannels * bytesPerSample)
	blockAlign := uint16(numChannels * bytesPerSample)
	bitsPerSample := uint16(bytesPerSample * 8)

	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, chunkSize)
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16)) // Subchunk1Size (16 for PCM)
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))  // AudioFormat (1 for PCM)
	_ = binary.Write(buf, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, byteRate)
	_ = binary.Write(buf, binary.LittleEndian, blockAlign)
	_ = binary.Write(buf, binary.LittleEndian, bitsPerSample)
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, dataSize)
	buf.Write(pcm)

	return buf.Bytes()
}
