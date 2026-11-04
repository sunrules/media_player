// go build -o mplayer.exe -ldflags "-s -w -H=windowsgui" 2>&1
// CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '-s -w' -o mplayer .
// go build -tiny -o mplayer.exe -ldflags "-s -w -H=windowsgui"  2>&1
// upx.exe --best .\mplayer.exe
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/eiannone/keyboard"
	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
	"github.com/faiface/beep/flac"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/faiface/beep/vorbis"
	"github.com/faiface/beep/wav"
)

// Playlist manages the playback list
type Playlist struct {
	files []string
	index int
	mu    sync.Mutex
}

// NewPlaylist creates a new playlist
func NewPlaylist() *Playlist {
	return &Playlist{
		files: make([]string, 0),
		index: -1,
	}
}

// AddFile adds a file to the playlist
func (pl *Playlist) AddFile(path string) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.files = append(pl.files, path)
}

// AddDirectory adds all audio files from a directory
func (pl *Playlist) AddDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	pl.mu.Lock()
	defer pl.mu.Unlock()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".mp3" || ext == ".flac" || ext == ".ogg" || ext == ".wav" {
			pl.files = append(pl.files, filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}

// Current returns the current file
func (pl *Playlist) Current() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.index < 0 || pl.index >= len(pl.files) {
		return ""
	}
	return pl.files[pl.index]
}

// Next moves to the next file
func (pl *Playlist) Next() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if len(pl.files) == 0 {
		return ""
	}
	pl.index = (pl.index + 1) % len(pl.files)
	return pl.files[pl.index]
}

// Prev moves to the previous file
func (pl *Playlist) Prev() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if len(pl.files) == 0 {
		return ""
	}
	pl.index = (pl.index - 1 + len(pl.files)) % len(pl.files)
	return pl.files[pl.index]
}

// IndexOf возвращает индекс файла в плейлисте или -1
func (pl *Playlist) IndexOf(path string) int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	for i, p := range pl.files {
		if p == path {
			return i
		}
	}
	return -1
}

// Files returns a copy of the playlist files
func (pl *Playlist) Files() []string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	out := make([]string, len(pl.files))
	copy(out, pl.files)
	return out
}

// Len returns the number of files
func (pl *Playlist) Len() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return len(pl.files)
}

// Index returns the current index
func (pl *Playlist) Index() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.index
}

// SetIndex sets the current index
func (pl *Playlist) SetIndex(i int) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if i >= 0 && i < len(pl.files) {
		pl.index = i
	}
}

// RemoveAt удаляет трек по индексу. Возвращает true, если удаление выполнено.
func (pl *Playlist) RemoveAt(i int) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if i < 0 || i >= len(pl.files) {
		return false
	}
	pl.files = append(pl.files[:i], pl.files[i+1:]...)
	if len(pl.files) == 0 {
		pl.index = -1
		return true
	}
	if pl.index > i {
		pl.index--
	} else if pl.index == i {
		if pl.index >= len(pl.files) {
			pl.index = len(pl.files) - 1
		}
	}
	return true
}

// Clear очищает плейлист.
func (pl *Playlist) Clear() {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.files = pl.files[:0]
	pl.index = -1
}

// AddFiles добавляет несколько файлов.
func (pl *Playlist) AddFiles(paths []string) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	for _, p := range paths {
		pl.files = append(pl.files, p)
	}
}

// Save сохраняет плейлист в файл (по одному пути на строку).
func (pl *Playlist) Save(path string) error {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, p := range pl.files {
		if _, err := fmt.Fprintln(f, p); err != nil {
			return err
		}
	}
	return nil
}

// LoadPlaylist загружает плейлист из файла, заменяя текущий.
func (pl *Playlist) LoadPlaylist(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	baseDir := filepath.Dir(path)
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.files = pl.files[:0]
	pl.index = -1
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !filepath.IsAbs(line) {
			line = filepath.Join(baseDir, line)
		}
		ext := strings.ToLower(filepath.Ext(line))
		if ext != ".mp3" && ext != ".flac" && ext != ".ogg" && ext != ".wav" {
			continue
		}
		if _, err := os.Stat(line); err != nil {
			continue
		}
		pl.files = append(pl.files, line)
	}
	return nil
}

var (
	debugFlag   bool
	consoleFlag bool
	helpFlag    bool
	debugLogger *os.File
)

func init() {
	flag.BoolVar(&debugFlag, "debug", false, "Включить debug режим с логированием в debug.log")
	flag.BoolVar(&consoleFlag, "console", false, "Запуск в консольном режиме")
	flag.BoolVar(&helpFlag, "help", false, "Показать справку по командам")
}

func debugPrintf(format string, v ...interface{}) {
	if !debugFlag || debugLogger == nil {
		return
	}
	_, _ = fmt.Fprintf(debugLogger, "[%s] %s\n", time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf(format, v...))
	debugLogger.Sync()
}

func debugPrintErr(err error, format string, v ...interface{}) {
	if !debugFlag || debugLogger == nil {
		return
	}
	msg := fmt.Sprintf(format, v...)
	if err != nil {
		msg = fmt.Sprintf("%s: %v", msg, err)
	}
	_, _ = fmt.Fprintf(debugLogger, "[%s] ERROR: %s\n", time.Now().Format("2006/01/02 15:04:05"), msg)
	debugLogger.Sync()
}

// Player - media player using beep
type Player struct {
	mu sync.Mutex

	// Audio
	streamer      beep.StreamSeeker
	ctrl          *beep.Ctrl
	format        beep.Format
	speakerInited bool
	analyzer      *spectrumAnalyzer

	// State
	isPlaying      bool
	volume         float64
	volumeStreamer *effects.Volume

	// UI elements
	trackLabel       *widget.Label
	statusLabel      *widget.Label
	volumeValueLabel *widget.Label
	visualizer       *canvas.Raster
	volumeSlider     *widget.Slider
	progressBar      *widget.Slider
	currentTimeLabel *widget.Label
	totalTimeLabel   *widget.Label
	window           fyne.Window

	// Time and progress
	totalSamples   int
	sampleRate     beep.SampleRate
	startTime      time.Time
	elapsedAtPause time.Duration
	stopProgress   chan struct{}
	progressActive bool

	// File
	filePath   string
	fileFormat string
	fileSize   int64

	// Playlist
	playlist *Playlist

	// Callback on track end
	onTrackEnd func()
}

// NewPlayer creates a new player
func NewPlayer(window fyne.Window) *Player {
	debugPrintf("Creating new player")
	return &Player{
		volume: 0.5,
		window: window,
	}
}

// SetVolume sets the volume
func (p *Player) SetVolume(volume float64) {
	debugPrintf("Setting volume: %.2f", volume)
	p.mu.Lock()
	defer p.mu.Unlock()

	p.volume = volume
	if p.volumeStreamer != nil {
		// Convert linear volume to decibels for better control
		if volume <= 0 {
			p.volumeStreamer.Silent = true
		} else {
			p.volumeStreamer.Silent = false
			// Logarithmic volume scale
			p.volumeStreamer.Volume = -5 + volume*5
		}
	}
}

// formatTime formats time to MM:SS
func (p *Player) formatTime(seconds float64) string {
	total := int(seconds)
	minutes := total / 60
	secondsRemaining := total % 60
	return fmt.Sprintf("%02d:%02d", minutes, secondsRemaining)
}

// updateTimeDisplay updates the time display
func (p *Player) updateTimeDisplay(currentSeconds, totalSeconds float64) {
	if p.currentTimeLabel != nil && p.totalTimeLabel != nil {
		fyne.Do(func() {
			p.currentTimeLabel.SetText(p.formatTime(currentSeconds))
			p.totalTimeLabel.SetText(p.formatTime(totalSeconds))
		})
	}
}

// Load loads an audio file
func (p *Player) Load(path string) error {
	debugPrintf("Loading file: %s", path)
	p.mu.Lock()
	defer p.mu.Unlock()

	// Stop current playback
	p.stopPlayback()

	// Open file
	file, err := os.Open(path)
	if err != nil {
		debugPrintErr(err, "Error opening file")
		return fmt.Errorf("failed to open file: %w", err)
	}

	// Get file size (Stat must happen before Close)
	if fi, err := file.Stat(); err == nil {
		p.fileSize = fi.Size()
	}

	// Determine format and create decoder
	ext := getFileExtension(path)
	var streamer beep.StreamSeeker
	var format beep.Format

	switch ext {
	case "mp3":
		s, f, err := mp3.Decode(file)
		if err != nil {
			file.Close()
			debugPrintErr(err, "Error decoding MP3")
			return fmt.Errorf("error decoding MP3: %w", err)
		}
		streamer = s
		format = f

	case "flac":
		s, f, err := flac.Decode(file)
		if err != nil {
			file.Close()
			debugPrintErr(err, "Error decoding FLAC")
			return fmt.Errorf("error decoding FLAC: %w", err)
		}
		streamer = s
		format = f

	case "ogg":
		s, f, err := vorbis.Decode(file)
		if err != nil {
			file.Close()
			debugPrintErr(err, "Error decoding OGG")
			return fmt.Errorf("error decoding OGG: %w", err)
		}
		streamer = s
		format = f

	case "wav":
		s, f, err := wav.Decode(file)
		if err != nil {
			file.Close()
			debugPrintErr(err, "Error decoding WAV")
			return fmt.Errorf("error decoding WAV: %w", err)
		}
		buf := beep.NewBuffer(f)
		buf.Append(s)
		decErr := s.Err()
		s.Close() // closes file
		if decErr != nil {
			return fmt.Errorf("error reading WAV: %w", decErr)
		}
		streamer = buf.Streamer(0, buf.Len())
		format = f

	default:
		file.Close()
		return fmt.Errorf("unsupported format: %s", ext)
	}

	// Initialize speaker if needed or if sample rate changed
	if !p.speakerInited || p.sampleRate != format.SampleRate {
		if p.speakerInited {
			speaker.Close()
		}

		// Initialize speaker with 100ms buffer for smooth playback
		err := speaker.Init(format.SampleRate, format.SampleRate.N(time.Millisecond*100))
		if err != nil {
			debugPrintErr(err, "Error initializing speaker")
			return fmt.Errorf("audio initialization error: %w", err)
		}
		p.speakerInited = true
		p.sampleRate = format.SampleRate
	}

	// Save state
	p.streamer = streamer
	p.format = format
	p.filePath = path
	p.fileFormat = ext
	p.totalSamples = streamer.Len()

	// Calculate duration
	duration := format.SampleRate.D(p.totalSamples).Seconds()
	debugPrintf("Track duration: %.2f seconds, format: %s, samples: %d, rate: %d Hz",
		duration, p.fileFormat, p.totalSamples, format.SampleRate)

	// Reset state
	p.isPlaying = false
	p.elapsedAtPause = 0

	// Update UI
	if p.trackLabel != nil {
		fyne.Do(func() { p.trackLabel.SetText(filepath.Base(path)) })
	}
	if p.statusLabel != nil {
		fyne.Do(func() { p.statusLabel.SetText("Stopped") })
	}
	if p.progressBar != nil {
		fyne.Do(func() {
			p.progressBar.Value = 0
			p.progressBar.Refresh()
			p.updateTimeDisplay(0, duration)
		})
	} else {
		p.updateTimeDisplay(0, duration)
	}

	return nil
}

// getFileExtension returns the file extension in lowercase
func getFileExtension(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return strings.ToLower(path[i+1:])
		}
	}
	return ""
}

// stopPlayback stops playback
func (p *Player) stopPlayback() {
	if p.ctrl != nil {
		p.ctrl.Paused = true
	}

	if p.stopProgress != nil {
		select {
		case p.stopProgress <- struct{}{}:
		default:
		}
		p.stopProgress = nil
		p.progressActive = false
	}

	p.isPlaying = false
}

// Pause pauses playback
func (p *Player) Pause() {
	debugPrintf("Pause")
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.isPlaying || p.ctrl == nil {
		return
	}

	// Remember elapsed time
	if p.isPlaying {
		p.elapsedAtPause += time.Since(p.startTime)
	}

	p.ctrl.Paused = true
	p.isPlaying = false

	if p.statusLabel != nil {
		fyne.Do(func() { p.statusLabel.SetText("Paused") })
	}

	// Stop progress updates
	if p.stopProgress != nil {
		select {
		case p.stopProgress <- struct{}{}:
		default:
		}
		p.progressActive = false
	}
}

// Stop stops playback
func (p *Player) Stop() {
	debugPrintf("Stop")
	p.mu.Lock()
	defer p.mu.Unlock()

	p.stopPlayback()

	// Rewind to beginning
	if p.streamer != nil {
		_ = p.streamer.Seek(0)
	}

	p.elapsedAtPause = 0

	duration := p.sampleRate.D(p.totalSamples).Seconds()
	if p.progressBar != nil {
		fyne.Do(func() {
			p.progressBar.Value = 0
			p.progressBar.Refresh()
			p.updateTimeDisplay(0, duration)
		})
	} else {
		p.updateTimeDisplay(0, duration)
	}
	if p.statusLabel != nil {
		fyne.Do(func() { p.statusLabel.SetText("Stopped") })
	}
}

// Play starts playback
func (p *Player) Play() {
	debugPrintf("Play")
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streamer == nil {
		debugPrintf("Decoder not loaded, playback impossible")
		return
	}

	if p.isPlaying {
		debugPrintf("Playback already active")
		return
	}

	// Create controller
	p.ctrl = &beep.Ctrl{Streamer: p.streamer, Paused: false}

	// Тап для реального анализа спектра: перехватывает сэмплы до передачи в динамик
	an := newSpectrumAnalyzer(float64(p.format.SampleRate), p.format.NumChannels)
	tap := &tapStreamer{inner: p.ctrl, an: an}
	p.analyzer = an

	// Create volume effect
	p.volumeStreamer = &effects.Volume{
		Streamer: tap,
		Base:     2,
		Volume:   -5 + p.volume*5,
		Silent:   p.volume <= 0,
	}

	// Play through speaker
	speaker.Play(p.volumeStreamer)

	p.isPlaying = true
	p.startTime = time.Now()

	// Update UI status
	if p.statusLabel != nil {
		fyne.Do(func() { p.statusLabel.SetText("Playing") })
	}

	// Start progress updates
	if !p.progressActive {
		p.stopProgress = make(chan struct{}, 1)
		go p.updateProgress()
		p.progressActive = true
	}
}

// updateProgress updates playback progress
func (p *Player) updateProgress() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.mu.Lock()

			if !p.isPlaying || p.totalSamples <= 0 {
				p.mu.Unlock()
				continue
			}

			// Calculate current position
			elapsed := p.elapsedAtPause
			if p.isPlaying {
				elapsed += time.Since(p.startTime)
			}

			currentSeconds := elapsed.Seconds()
			duration := p.sampleRate.D(p.totalSamples).Seconds()
			progress := currentSeconds / duration

			if progress < 0 {
				progress = 0
				currentSeconds = 0
			}
			if progress >= 1 {
				progress = 1
				currentSeconds = duration
				p.mu.Unlock()

				// Track ended - call callback if exists
				if p.onTrackEnd != nil {
					p.onTrackEnd()
				} else {
					p.Stop()
				}
				continue
			}

			// Update UI
			if p.progressBar != nil {
				currentProgress := progress
				currentSecs := currentSeconds
				fyne.Do(func() {
					if p.progressBar != nil && p.progressBar.Value != currentProgress {
						p.progressBar.Value = currentProgress
						p.progressBar.Refresh()
					}
					p.updateTimeDisplay(currentSecs, duration)
				})
			}

			p.mu.Unlock()

		case <-p.stopProgress:
			return
		}
	}
}

// ShowInfo shows information about the loaded file
func (p *Player) ShowInfo(parent fyne.Window) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streamer == nil {
		dialog.ShowInformation("Information", "No file loaded", parent)
		return
	}

	duration := p.sampleRate.D(p.totalSamples).Seconds()

	// Format file size
	var sizeStr string
	if p.fileSize < 1024 {
		sizeStr = fmt.Sprintf("%d bytes", p.fileSize)
	} else if p.fileSize < 1024*1024 {
		sizeStr = fmt.Sprintf("%.2f KB", float64(p.fileSize)/1024)
	} else {
		sizeStr = fmt.Sprintf("%.2f MB", float64(p.fileSize)/(1024*1024))
	}

	// Get file name from path
	fileName := p.filePath
	for i := len(p.filePath) - 1; i >= 0; i-- {
		if p.filePath[i] == '\\' || p.filePath[i] == '/' {
			fileName = p.filePath[i+1:]
			break
		}
	}

	info := fmt.Sprintf(`File Information:

File name: %s
Format: %s
Size: %s

Sample rate: %d Hz
Channels: %d
Duration: %s

Total samples: %d`,
		fileName,
		strings.ToUpper(p.fileFormat),
		sizeStr,
		p.sampleRate,
		p.format.NumChannels,
		p.formatTime(duration),
		p.totalSamples,
	)

	dialog.ShowInformation("Media File Information", info, parent)
}

// ShowFileInfo shows information about the given file without loading it into the player
func (p *Player) ShowFileInfo(path string, parent fyne.Window) {
	file, err := os.Open(path)
	if err != nil {
		dialog.ShowError(err, parent)
		return
	}
	var fileSize int64
	if fi, err := file.Stat(); err == nil {
		fileSize = fi.Size()
	}

	ext := getFileExtension(path)
	var streamer beep.StreamSeeker
	var format beep.Format
	var closeFile bool = true

	switch ext {
	case "mp3":
		s, f, err := mp3.Decode(file)
		if err != nil {
			file.Close()
			dialog.ShowError(err, parent)
			return
		}
		streamer = s
		format = f
	case "flac":
		s, f, err := flac.Decode(file)
		if err != nil {
			file.Close()
			dialog.ShowError(err, parent)
			return
		}
		streamer = s
		format = f
	case "ogg":
		s, f, err := vorbis.Decode(file)
		if err != nil {
			file.Close()
			dialog.ShowError(err, parent)
			return
		}
		streamer = s
		format = f
	case "wav":
		s, f, err := wav.Decode(file)
		if err != nil {
			file.Close()
			dialog.ShowError(err, parent)
			return
		}
		buf := beep.NewBuffer(f)
		buf.Append(s)
		decErr := s.Err()
		s.Close() // closes file
		closeFile = false
		if decErr != nil {
			dialog.ShowError(decErr, parent)
			return
		}
		streamer = buf.Streamer(0, buf.Len())
		format = f
	default:
		file.Close()
		dialog.ShowError(fmt.Errorf("unsupported format: %s", ext), parent)
		return
	}

	totalSamples := streamer.Len()
	duration := format.SampleRate.D(totalSamples).Seconds()

	if closeFile {
		if c, ok := streamer.(io.Closer); ok {
			c.Close()
		}
	}

	var sizeStr string
	if fileSize < 1024 {
		sizeStr = fmt.Sprintf("%d bytes", fileSize)
	} else if fileSize < 1024*1024 {
		sizeStr = fmt.Sprintf("%.2f KB", float64(fileSize)/1024)
	} else {
		sizeStr = fmt.Sprintf("%.2f MB", float64(fileSize)/(1024*1024))
	}

	fileName := path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '\\' || path[i] == '/' {
			fileName = path[i+1:]
			break
		}
	}

	info := fmt.Sprintf(`File Information:

File name: %s
Format: %s
Size: %s

Sample rate: %d Hz
Channels: %d
Duration: %s

Total samples: %d`,
		fileName,
		strings.ToUpper(ext),
		sizeStr,
		format.SampleRate,
		format.NumChannels,
		p.formatTime(duration),
		totalSamples,
	)

	dialog.ShowInformation("Media File Information", info, parent)
}

// Seek seeks to position (0.0 - 1.0)
func (p *Player) Seek(pos float64) {
	debugPrintf("Seeking to position: %.2f", pos)
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streamer == nil || pos < 0 || pos > 1 {
		debugPrintf("Invalid parameters for seek")
		return
	}

	target := int(pos * float64(p.totalSamples))

	speaker.Lock()
	err := p.streamer.Seek(target)
	speaker.Unlock()
	if err != nil {
		debugPrintErr(err, "Seek error")
		return
	}

	duration := p.sampleRate.D(p.totalSamples).Seconds()
	p.elapsedAtPause = time.Duration(pos * duration * float64(time.Second))
	if p.isPlaying {
		p.startTime = time.Now()
	}

	currentSeconds := pos * duration
	if p.progressBar != nil {
		p.progressBar.Value = pos
		p.progressBar.Refresh()
	}
	p.updateTimeDisplay(currentSeconds, duration)
}

func showHelp() {
	fmt.Println(`Audio Player - console media player

USAGE:
  mplayer.exe [options] [files or folders]

OPTIONS:
  -console    Run in console mode
  -debug      Enable debug mode with logging to debug.log
  -help       Show this help

EXAMPLES:
  mplayer.exe song.mp3                    # Play one file
  mplayer.exe song1.mp3 song2.flac        # Play multiple files
  mplayer.exe C:\Music                    # Play all files from folder
  mplayer.exe -console C:\Music           # Console mode with folder

HOTKEYS (console mode):
  p       Pause / Resume
  s       Stop
  n       Next track
  r       Previous track
  +       Increase volume
  -       Decrease volume
  ←       Seek backward (5 seconds)
  →       Seek forward (5 seconds)
  x       Exit
  q       Exit
  Esc     Exit

SUPPORTED FORMATS:
  MP3, FLAC, OGG, WAV`)
}

func runConsoleMode(playlist *Playlist) {
	fmt.Println("=== Audio Player - Console Mode ===")
	fmt.Printf("Playlist: %d file(s)\n", playlist.Len())
	fmt.Println("Press 'h' for command help")
	fmt.Println("=========================================")

	player := NewPlayer(nil)
	player.playlist = playlist

	// Function to load and play next track
	playNextTrack := func() {
		file := playlist.Next()
		if file != "" {
			if err := player.Load(file); err != nil {
				fmt.Printf("Load error: %v\n", err)
			} else {
				fmt.Printf("▶ Next: %s\n", filepath.Base(file))
				player.Play()
			}
		} else {
			fmt.Println("Playlist ended")
		}
	}

	// Set callback for automatic track switching
	player.onTrackEnd = playNextTrack

	// Load first track
	if playlist.Len() > 0 {
		file := playlist.Next()
		if err := player.Load(file); err != nil {
			fmt.Printf("Load error: %v\n", err)
		} else {
			fmt.Printf("Loaded: %s\n", filepath.Base(file))
			player.Play()
		}
	}

	// Keyboard handling
	if err := keyboard.Open(); err != nil {
		fmt.Printf("Keyboard initialization error: %v\n", err)
		fmt.Println("Press Enter to exit...")
		fmt.Scanln()
		return
	}
	defer keyboard.Close()

	fmt.Println("Player started. Press 'h' for help.")

	for {
		char, key, err := keyboard.GetKey()
		if err != nil {
			fmt.Printf("Ошибка чтения клавиши: %v\n", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		switch {
		case key == keyboard.KeyEsc || char == 'x' || char == 'q':
			fmt.Println("Exiting...")
			player.Stop()
			return

		case char == 'p':
			player.mu.Lock()
			wasPlaying := player.isPlaying
			player.mu.Unlock()

			if wasPlaying {
				player.Pause()
				fmt.Println("⏸ Pause")
			} else {
				player.Play()
				fmt.Println("▶ Resume")
			}

		case char == 's':
			player.Stop()
			fmt.Println("⏹ Stop")

		case char == 'n':
			player.Stop()
			file := playlist.Next()
			if file != "" {
				if err := player.Load(file); err != nil {
					fmt.Printf("Load error: %v\n", err)
				} else {
					fmt.Printf("▶ Next: %s\n", filepath.Base(file))
					player.Play()
				}
			} else {
				fmt.Println("Playlist is empty")
			}

		case char == 'r':
			player.Stop()
			file := playlist.Prev()
			if file != "" {
				if err := player.Load(file); err != nil {
					fmt.Printf("Load error: %v\n", err)
				} else {
					fmt.Printf("▶ Previous: %s\n", filepath.Base(file))
					player.Play()
				}
			} else {
				fmt.Println("Playlist is empty")
			}

		case char == '+':
			player.mu.Lock()
			newVol := player.volume + 0.1
			if newVol > 1.0 {
				newVol = 1.0
			}
			player.mu.Unlock()
			player.SetVolume(newVol)
			fmt.Printf("🔊 Volume: %d%%\n", int(newVol*100))

		case char == '-':
			player.mu.Lock()
			newVol := player.volume - 0.1
			if newVol < 0 {
				newVol = 0
			}
			player.mu.Unlock()
			player.SetVolume(newVol)
			fmt.Printf("🔉 Volume: %d%%\n", int(newVol*100))

		case key == keyboard.KeyArrowLeft:
			player.mu.Lock()
			if player.streamer != nil && player.totalSamples > 0 {
				currentPos := float64(player.streamer.Position()) / float64(player.totalSamples)
				duration := player.sampleRate.D(player.totalSamples).Seconds()
				newPos := currentPos - 5.0/duration
				if newPos < 0 {
					newPos = 0
				}
				player.mu.Unlock()
				player.Seek(newPos)
				fmt.Printf("⏪ Seek backward\n")
			} else {
				player.mu.Unlock()
			}

		case key == keyboard.KeyArrowRight:
			player.mu.Lock()
			if player.streamer != nil && player.totalSamples > 0 {
				currentPos := float64(player.streamer.Position()) / float64(player.totalSamples)
				duration := player.sampleRate.D(player.totalSamples).Seconds()
				newPos := currentPos + 5.0/duration
				if newPos > 1 {
					newPos = 1
				}
				player.mu.Unlock()
				player.Seek(newPos)
				fmt.Printf("⏩ Seek forward\n")
			} else {
				player.mu.Unlock()
			}

		case char == 'h':
			fmt.Println(`
Commands:
  p - Pause/Resume
  s - Stop
  n - Next track
  r - Previous track
  + - Increase volume
  - - Decrease volume
  ← - Seek backward (5 sec)
  → - Seek forward (5 sec)
  x/q/Esc - Exit`)
		}
	}
}

// ---------- Пользовательский диалог открытия файлов ----------

const entryPC = "💻 Мой компьютер" // спецзапись: переход к списку дисков

type browseEntry struct {
	isDir bool
	name  string
	path  string // готовый путь для спецзаписей (диск, «Домой»)
	toPC  bool   // переход к списку дисков
}

type browseState struct {
	dir           string
	items         []browseEntry
	selected      int
	selectedFiles map[int]bool
}

var lastOpenDir = "" // запоминаем последнюю папку между запусками диалога

func showFileOpenDialog(parent fyne.Window, onConfirm func(paths []string)) {
	a := fyne.CurrentApp()
	w := a.NewWindow("Открыть аудиофайл")
	w.Resize(fyne.NewSize(760, 540)) // начальный размер, окно можно растянуть
	w.CenterOnScreen()

	startDir := lastOpenDir
	if startDir == "" {
		if d, err := os.UserHomeDir(); err == nil {
			startDir = d
		} else {
			startDir = "."
		}
	}

	st := &browseState{dir: startDir, selected: -1, selectedFiles: make(map[int]bool)}

	pathLabel := widget.NewLabel(displayBrowseDir(st.dir))
	statusLabel := widget.NewLabel("") // здесь виден ПОЛНЫЙ путь — длинные имена не теряются

	list := widget.NewList(
		func() int { return len(st.items) },
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			e := st.items[id]
			l := obj.(*widget.Label)
			if e.isDir {
				l.TextStyle = fyne.TextStyle{Bold: true} // папки — жирным
			} else {
				l.TextStyle = fyne.TextStyle{}
			}
			name := e.name
			if !e.isDir && st.selectedFiles[int(id)] {
				name = "✓ " + name
			}
			l.SetText(name)
		},
	)

	openBtn := widget.NewButton("Открыть", func() {
		var paths []string
		for id, ok := range st.selectedFiles {
			if !ok || id < 0 || id >= len(st.items) {
				continue
			}
			e := st.items[id]
			if e.isDir {
				continue
			}
			var full string
			switch {
			case e.path != "":
				full = e.path
			default:
				full = filepath.Join(st.dir, e.name)
			}
			paths = append(paths, full)
		}
		if len(paths) == 0 {
			return
		}
		lastOpenDir = st.dir
		w.Close()
		onConfirm(paths)
	})
	openBtn.Disable()

	upBtn := widget.NewButton("⬆ Вверх", func() {
		if st.dir == "" { // уже на списке дисков — выше некуда
			return
		}
		parentDir := filepath.Dir(st.dir)
		if parentDir != st.dir {
			st.dir = parentDir
		} else {
			st.dir = "" // с корня диска поднимаемся к списку дисков
		}
		pathLabel.SetText(displayBrowseDir(st.dir))
		loadBrowseDir(list, st)
	})
	cancelBtn := widget.NewButton("Отмена", func() { w.Close() })

	list.OnSelected = func(id widget.ListItemID) {
		if id >= len(st.items) {
			return
		}
		e := st.items[id]
		var full string
		switch {
		case e.toPC:
			full = "" // список дисков
		case e.path != "":
			full = e.path
		default:
			full = filepath.Join(st.dir, e.name)
		}
		statusLabel.SetText(displayBrowseDir(full)) // полное имя всегда видно внизу
		st.selected = int(id)

		if e.isDir {
			openBtn.Disable()
			st.dir = full
			pathLabel.SetText(displayBrowseDir(st.dir))
			loadBrowseDir(list, st)
			return
		}
		// toggle selection for file
		if st.selectedFiles[int(id)] {
			delete(st.selectedFiles, int(id))
		} else {
			st.selectedFiles[int(id)] = true
		}
		// enable open button if any selected
		if len(st.selectedFiles) > 0 {
			openBtn.Enable()
		} else {
			openBtn.Disable()
		}
		list.Unselect(id)
		list.Refresh()
	}

	toolbar := container.NewHBox(upBtn, openBtn, cancelBtn)
	content := container.NewBorder(
		container.NewVBox(pathLabel, toolbar), // верх
		statusLabel,                           // низ: полный путь
		nil, nil, list,
	)
	w.SetContent(content)

	loadBrowseDir(list, st)
	w.Show() // ShowAndRun вызывать нельзя — он заблокирует основной цикл главного окна
}

// showAddToPlaylistDialog открывает диалог выбора файлов/папок для добавления в плейлист
func showAddToPlaylistDialog(parent fyne.Window, onConfirm func(paths []string)) {
	a := fyne.CurrentApp()
	w := a.NewWindow("Добавить в плейлист")
	w.Resize(fyne.NewSize(520, 400))
	w.CenterOnScreen()

	st := &browseState{dir: lastOpenDir, selected: -1}
	if st.dir == "" {
		st.dir = entryPC
	}
	lastOpenDir = st.dir

	pathLabel := widget.NewLabel(displayBrowseDir(st.dir))
	pathLabel.TextStyle = fyne.TextStyle{Bold: true}
	statusLabel := widget.NewLabel("")
	var list *widget.List
	upBtn := widget.NewButton("⬆ Вверх", func() {
		if st.dir == "" || st.dir == entryPC {
			st.dir = entryPC
		} else {
			st.dir = filepath.Dir(st.dir)
		}
		pathLabel.SetText(displayBrowseDir(st.dir))
		loadBrowseDir(list, st)
	})
	openBtn := widget.NewButtonWithIcon("Добавить", theme.ConfirmIcon(), func() {
		var paths []string
		if st.selected >= 0 && st.selected < len(st.items) {
			if st.items[st.selected].isDir {
				entries, err := os.ReadDir(st.items[st.selected].path)
				if err != nil {
					dialog.ShowError(err, w)
					return
				}
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					ext := strings.ToLower(filepath.Ext(e.Name()))
					if ext == ".mp3" || ext == ".flac" || ext == ".ogg" || ext == ".wav" {
						paths = append(paths, filepath.Join(st.items[st.selected].path, e.Name()))
					}
				}
			} else {
				paths = append(paths, st.items[st.selected].path)
			}
		}
		if len(paths) == 0 {
			return
		}
		w.Close()
		onConfirm(paths)
	})
	cancelBtn := widget.NewButtonWithIcon("Отмена", theme.CancelIcon(), func() { w.Close() })
	list = widget.NewList(
		func() int { return len(st.items) },
		func() fyne.CanvasObject { return widget.NewLabel("item") },
		func(id int, o fyne.CanvasObject) {
			if id < len(st.items) {
				o.(*widget.Label).SetText(st.items[id].name)
			}
		},
	)
	list.OnSelected = func(id int) {
		st.selected = id
		if id < len(st.items) {
			if st.items[id].isDir {
				st.dir = st.items[id].path
				pathLabel.SetText(displayBrowseDir(st.dir))
				loadBrowseDir(list, st)
				return
			}
		}
		openBtn.Enable()
	}

	toolbar := container.NewHBox(upBtn, openBtn, cancelBtn)
	content := container.NewBorder(
		container.NewVBox(pathLabel, toolbar),
		statusLabel,
		nil, nil, list,
	)
	w.SetContent(content)
	loadBrowseDir(list, st)
	w.Show()
}

func displayBrowseDir(dir string) string {
	if dir == "" {
		return entryPC
	}
	return dir
}

// browseDrives — содержимое экрана «Мой компьютер»: диски и домашняя папка
func browseDrives() []browseEntry {
	var items []browseEntry
	if home, err := os.UserHomeDir(); err == nil {
		items = append(items, browseEntry{isDir: true, name: "⌂ Домой", path: home})
	}
	hasDrive := false
	for c := 'A'; c <= 'Z'; c++ {
		drivePath := fmt.Sprintf("%c:\\", c)
		if info, err := os.Stat(drivePath); err == nil && info.IsDir() {
			items = append(items, browseEntry{isDir: true, name: string(c) + ":", path: drivePath})
			hasDrive = true
		}
	}
	if !hasDrive { // не Windows — просто корень файловой системы
		items = append(items, browseEntry{isDir: true, name: "/", path: "/"})
	}
	return items
}

func loadBrowseDir(list *widget.List, st *browseState) {
	var newItems []browseEntry
	if st.dir == "" {
		newItems = browseDrives()
	} else {
		entries, err := os.ReadDir(st.dir)
		if err == nil {
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") {
					continue
				}
				if e.IsDir() {
					newItems = append(newItems, browseEntry{isDir: true, name: e.Name()})
				} else {
					ext := strings.ToLower(filepath.Ext(e.Name()))
					if ext == ".mp3" || ext == ".flac" || ext == ".ogg" || ext == ".wav" {
						newItems = append(newItems, browseEntry{name: e.Name()})
					}
				}
			}
			sort.SliceStable(newItems, func(i, j int) bool {
				if newItems[i].isDir != newItems[j].isDir {
					return newItems[i].isDir // сначала папки
				}
				return strings.ToLower(newItems[i].name) < strings.ToLower(newItems[j].name)
			})
		}
		newItems = append([]browseEntry{{isDir: true, name: entryPC, toPC: true}}, newItems...)
	}
	st.items = newItems
	st.selected = -1
	st.selectedFiles = make(map[int]bool)
	list.UnselectAll()
	list.Refresh()
}

// ---------- Конец диалога ----------

// spectrumWidget - simple animated spectrum visualizer in Winamp style
type spectrumWidget struct {
	widget.BaseWidget
	bars  []float64
	mu    sync.Mutex
	color color.Color
}

func newSpectrumWidget(bars int) *spectrumWidget {
	s := &spectrumWidget{
		bars:  make([]float64, bars),
		color: color.RGBA{R: 0x00, G: 0xFF, B: 0x66, A: 0xFF},
	}
	s.ExtendBaseWidget(s)
	return s
}

type spectrumRenderer struct {
	widget *spectrumWidget
	rects  []*canvas.Rectangle
}

func (s *spectrumWidget) CreateRenderer() fyne.WidgetRenderer {
	r := &spectrumRenderer{widget: s, rects: make([]*canvas.Rectangle, len(s.bars))}
	for i := range r.rects {
		r.rects[i] = canvas.NewRectangle(s.color)
	}
	return r
}

func (r *spectrumRenderer) Layout(size fyne.Size) {
	n := len(r.rects)
	if n == 0 {
		return
	}
	r.widget.mu.Lock()
	bars := make([]float64, len(r.widget.bars))
	copy(bars, r.widget.bars)
	r.widget.mu.Unlock()

	barW := size.Width / float32(n)
	gap := float32(2)
	for i, rect := range r.rects {
		x := float32(i) * barW
		h := float32(bars[i]) * size.Height
		if h < 2 {
			h = 2
		}
		rect.Move(fyne.NewPos(x, size.Height-h))
		rect.Resize(fyne.NewSize(barW-gap, h))
	}
}

func (r *spectrumRenderer) MinSize() fyne.Size {
	return fyne.NewSize(100, 60)
}

func (r *spectrumRenderer) Refresh() {
	r.widget.mu.Lock()
	defer r.widget.mu.Unlock()
	for _, rect := range r.rects {
		rect.FillColor = r.widget.color
		canvas.Refresh(rect)
	}
	canvas.Refresh(r.widget)
}

func (r *spectrumRenderer) Objects() []fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, len(r.rects))
	for i, rct := range r.rects {
		objs[i] = rct
	}
	return objs
}

func (r *spectrumRenderer) Destroy() {}

func (s *spectrumWidget) SetBars(vals []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(vals) != len(s.bars) {
		return
	}
	copy(s.bars, vals)
	s.Refresh()
}

func (s *spectrumWidget) Decay() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.bars {
		s.bars[i] *= 0.85
		if s.bars[i] < 0.02 {
			s.bars[i] = 0
		}
	}
	s.Refresh()
}

// spectrumData backs a canvas.Raster visualizer
type spectrumData struct {
	mu   sync.Mutex
	bars []float64
}

func newSpectrumData(n int) *spectrumData {
	return &spectrumData{bars: make([]float64, n)}
}

func (s *spectrumData) SetBars(v []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(v) != len(s.bars) {
		return
	}
	copy(s.bars, v)
}

func (s *spectrumData) Decay() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.bars {
		s.bars[i] *= 0.85
		if s.bars[i] < 0.02 {
			s.bars[i] = 0
		}
	}
}

func (s *spectrumData) Draw(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// background
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 0x10, G: 0x10, B: 0x10, A: 0xFF})
		}
	}
	s.mu.Lock()
	bars := make([]float64, len(s.bars))
	copy(bars, s.bars)
	s.mu.Unlock()

	n := len(bars)
	if n == 0 || w <= 0 || h <= 0 {
		return img
	}
	barW := w / n
	for i, b := range bars {
		bh := int(b * float64(h))
		if bh < 1 {
			bh = 1
		}
		x0 := i * barW
		x1 := x0 + barW - 2
		if x1 >= w {
			x1 = w - 1
		}
		for x := x0; x <= x1; x++ {
			for y := h - bh; y < h; y++ {
				img.Set(x, y, color.RGBA{R: 0x00, G: 0xFF, B: 0x66, A: 0xFF})
			}
		}
	}
	return img
}

// ---------- Реальный анализ спектра (FFT) ----------

const fftSize = 2048 // размер окна FFT, степень двойки
const hopSize = fftSize / 4

// Диапазон частот для шкалы (как у большинства визуализаторов: лог-шкала от басов до верха)
const minSpectrumFreqHz = 35.0
const maxSpectrumFreqHz = 16000.0

// tapStreamer перехватывает сэмплы, идущие в динамик, и передаёт их анализатору спектра
type tapStreamer struct {
	inner beep.Streamer
	an    *spectrumAnalyzer
}

func (t *tapStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	n, ok = t.inner.Stream(samples)
	if n > 0 && t.an != nil {
		t.an.AddSamples(samples[:n])
	}
	return n, ok
}

func (t *tapStreamer) Err() error { return t.inner.Err() }

// spectrumAnalyzer накапливает сэмплы в кольцевом буфере и считает амплитудный спектр.
// AddSamples вызывается из аудио-потока beep, Update — из потока GUI.
type spectrumAnalyzer struct {
	mu         sync.Mutex
	sampleRate float64
	channels   int
	ring       []int16 // кольцевой буфер моно-сэмплов
	writeIdx   int
	written    uint64
	consumed   uint64

	re, im     []float64 // рабочее пространство FFT (перисутуется)
	twRe, twIm []float64 // готовые таблички поворотных множителей (twiddle)
	win        []float64 // окно Ханна
	nBars      int
	mags       []float64 // амплитуды текущего кадра по барам
	disp       []float64 // сглаженные значения по барам
	globalPeak float64   // глобальный подвижной пик для автонормировки
}

// newSpectrumAnalyzer создаёт анализатор под частоту дискретизации и число каналов дорожки
func newSpectrumAnalyzer(sampleRate float64, channels int) *spectrumAnalyzer {
	if channels < 1 {
		channels = 1
	}
	a := &spectrumAnalyzer{
		sampleRate: sampleRate,
		channels:   channels,
		ring:       make([]int16, fftSize*8),
		re:         make([]float64, fftSize),
		im:         make([]float64, fftSize),
		twRe:       make([]float64, fftSize/2+1),
		twIm:       make([]float64, fftSize/2+1),
		win:        make([]float64, fftSize),
	}
	for j := 0; j <= fftSize/2; j++ {
		angle := -2 * math.Pi * float64(j) / float64(fftSize)
		a.twRe[j] = math.Cos(angle)
		a.twIm[j] = math.Sin(angle)
	}
	for i := 0; i < fftSize; i++ {
		a.win[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(fftSize-1)))
	}
	return a
}

// AddSamples добавляет сэмплы из аудио-потока в кольцевой буфер (моно-миксдаун)
func (a *spectrumAnalyzer) AddSamples(s [][2]float64) {
	ch := a.channels
	capacity := len(a.ring)
	a.mu.Lock()
	if a.written-a.consumed >= uint64(capacity) {
		a.consumed = a.written // буфер переполнен — старые сэмплы не нужны
	}
	for i := range s {
		v := s[i][0]
		if ch > 1 {
			v = (v + s[i][1]) / 2.0
		}
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		a.ring[a.writeIdx] = int16(v * 32767)
		a.writeIdx++
		if a.writeIdx == capacity {
			a.writeIdx = 0
		}
		a.written++
	}
	a.mu.Unlock()
}

// Update вычисляет нормализованные уровни баров из последнего окна сэмплов.
// Если новых данных нет, значения плавно затухают.
func (a *spectrumAnalyzer) Update(out []float64) {
	nb := len(out)
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.nBars != nb {
		a.mags = make([]float64, nb)
		a.disp = make([]float64, nb)
		a.nBars = nb
	}

	if a.written < fftSize || int(a.written-a.consumed) < hopSize {
		for i := range out {
			d := a.disp[i] * 0.75
			if d < 0.01 {
				d = 0
			}
			a.disp[i] = d
			out[i] = d
		}
		return
	}

	start := int(a.written) - fftSize
	capacity := len(a.ring)
	for i := 0; i < fftSize; i++ {
		idx := (start + i) % capacity
		a.re[i] = float64(a.ring[idx]) / 32768.0 * a.win[i]
		a.im[i] = 0
	}
	a.consumed += hopSize

	a.fft()

	binHz := a.sampleRate / float64(fftSize)
	half := fftSize / 2
	ratio := maxSpectrumFreqHz / minSpectrumFreqHz
	perBar := math.Pow(ratio, 1.0/float64(nb))
	// Первый проход: амплитуда каждого бара (максимум по бинам FFT в диапазоне частот бара)
	maxAmp := 0.0
	for b := 0; b < nb; b++ {
		fLo := minSpectrumFreqHz * math.Pow(perBar, float64(b))
		fHi := minSpectrumFreqHz * math.Pow(perBar, float64(b+1))
		kLo := int(fLo / binHz)
		kHi := int(fHi / binHz)
		if kLo < 1 {
			kLo = 1
		}
		if kHi <= kLo {
			kHi = kLo + 1
		}
		if kHi > half-1 {
			kHi = half - 1
		}
		mag := 0.0
		for k := kLo; k < kHi; k++ {
			m := math.Sqrt(a.re[k]*a.re[k] + a.im[k]*a.im[k])
			if m > mag {
				mag = m
			}
		}
		a.mags[b] = 0.5 * mag / float64(fftSize)
		if a.mags[b] > maxAmp {
			maxAmp = a.mags[b]
		}
	}

	// Глобальный подвижной пик: громчайший компонент кадра показывает полный уровень,
	// остальные — пропорционально (классическая автонормировка Winamp)
	globalPeak := a.globalPeak * 0.92
	if maxAmp > globalPeak {
		globalPeak = maxAmp
	}
	a.globalPeak = globalPeak

	// Второй проход: нормализация и сглаживание (быстрая атака, плавный спад)
	for b := 0; b < nb; b++ {
		var v float64
		if globalPeak > 1e-7 {
			v = a.mags[b] / globalPeak
		}
		if v > 1 {
			v = 1
		} else if v < 0.02 {
			v = 0
		}
		d := math.Max(v, a.disp[b]*0.75)
		a.disp[b] = d
		out[b] = d
	}
}

// fft — итеративное БПФ Кули–Тьюки (in-place), таблички поворотных множителей заранее
func (a *spectrumAnalyzer) fft() {
	re, im := a.re, a.im
	n := len(re)

	for i, j := 1, 0; i < n-1; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j |= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}

	for length := 2; length <= n; length <<= 1 {
		half := length >> 1
		skip := n / length
		for i := 0; i < n; i += length {
			for k := 0; k < half; k++ {
				wr := a.twRe[k*skip]
				wi := a.twIm[k*skip]
				j := i + k
				m := j + half
				tre := re[m]*wr - im[m]*wi
				tim := re[m]*wi + im[m]*wr
				re[m] = re[j] - tre
				im[m] = im[j] - tim
				re[j] += tre
				im[j] += tim
			}
		}
	}
}

// deleteShortcut implements fyne.KeyboardShortcut for the Delete key.
type deleteShortcut struct{}

func (deleteShortcut) ShortcutName() string { return "DeleteTrack" }

func (deleteShortcut) Key() fyne.KeyName { return fyne.KeyDelete }

func (deleteShortcut) Mod() fyne.KeyModifier { return 0 }

func main() {
	flag.Parse()

	// Handle help before debug initialization
	if helpFlag {
		showHelp()
		return
	}

	if debugFlag {
		var err error
		debugLogger, err = os.OpenFile("debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			fmt.Printf("Error opening debug.log: %v\n", err)
			debugFlag = false
		} else {
			defer debugLogger.Close()
		}
	}
	debugPrintf("Starting Audio Player (debug: %v, console: %v)", debugFlag, consoleFlag)

	// Create playlist from command line arguments
	playlist := NewPlaylist()
	args := flag.Args()

	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		if info.IsDir() {
			if err := playlist.AddDirectory(arg); err != nil {
				fmt.Printf("Error reading directory: %v\n", err)
			}
		} else {
			ext := strings.ToLower(filepath.Ext(arg))
			if ext == ".mp3" || ext == ".flac" || ext == ".ogg" || ext == ".wav" {
				playlist.AddFile(arg)
			}
		}
	}

	// Console mode
	if consoleFlag {
		if playlist.Len() == 0 {
			fmt.Println("Error: no files specified for playback")
			fmt.Println("Use: mplayer.exe -help for help")
			return
		}
		runConsoleMode(playlist)
		return
	}

	a := app.NewWithID("com.example.audioplayer")
	a.Settings().SetTheme(theme.DarkTheme())
	w := a.NewWindow("Media Player")
	w.Resize(fyne.NewSize(520, 460))

	player := NewPlayer(w)
	player.playlist = playlist

	// Visualizer
	specData := newSpectrumData(28)
	visualizer := canvas.NewRaster(specData.Draw)
	visualizer.SetMinSize(fyne.NewSize(0, 70))
	player.visualizer = visualizer

	trackLabel := widget.NewLabel("No track loaded")
	trackLabel.TextStyle = fyne.TextStyle{Bold: true}
	player.trackLabel = trackLabel

	statusLabel := widget.NewLabel("Stopped")
	player.statusLabel = statusLabel

	volumeValueLabel := widget.NewLabel("50%")
	player.volumeValueLabel = volumeValueLabel

	selectedPlaylistIndex := -1
	lastTapIndex := -1
	var lastTapTime time.Time

	// Playlist UI
	playlistList := widget.NewList(
		func() int { return len(player.playlist.Files()) },
		func() fyne.CanvasObject { return widget.NewLabel("track") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			files := player.playlist.Files()
			if i >= 0 && i < len(files) {
				label := o.(*widget.Label)
				if int(i) == selectedPlaylistIndex {
					label.TextStyle = fyne.TextStyle{Bold: true}
					label.SetText("▶ " + filepath.Base(files[i]))
				} else {
					label.TextStyle = fyne.TextStyle{}
					label.SetText(filepath.Base(files[i]))
				}
			}
		},
	)
	playlistScroll := container.NewScroll(playlistList)
	playlistScroll.SetMinSize(fyne.NewSize(0, 150))

	syncing := false

	refreshPlaylist := func() {
		fyne.Do(func() {
			old := player.playlist.Index()
			selectedPlaylistIndex = old
			playlistList.Refresh()
		})
	}

	openBtn := widget.NewButtonWithIcon("Open", theme.FolderOpenIcon(), func() {
		showFileOpenDialog(w, func(paths []string) {
			if len(paths) == 0 {
				return
			}
			go func() {
				player.playlist.AddFiles(paths)
				idx := player.playlist.IndexOf(paths[0])
				if idx >= 0 {
					player.playlist.SetIndex(idx)
				}
				refreshPlaylist()
				if err := player.Load(paths[0]); err != nil {
					fyne.Do(func() { dialog.ShowError(err, w) })
				} else {
					player.Play()
					fyne.Do(func() {
						syncing = true
						selectedPlaylistIndex = player.playlist.Index()
						playlistList.Refresh()
						syncing = false
					})
				}
			}()
		})
	})

	prevBtn := widget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), func() {
		go func() {
			player.Stop()
			file := player.playlist.Prev()
			if file != "" {
				if err := player.Load(file); err != nil {
					fyne.Do(func() { dialog.ShowError(err, w) })
				} else {
					player.Play()
					fyne.Do(func() {
						syncing = true
						selectedPlaylistIndex = player.playlist.Index()
						playlistList.Refresh()
						syncing = false
					})
				}
			}
		}()
	})
	playBtn := widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() { player.Play() })
	pauseBtn := widget.NewButtonWithIcon("", theme.MediaPauseIcon(), func() { player.Pause() })
	stopBtn := widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() { player.Stop() })
	nextBtn := widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() {
		go func() {
			player.Stop()
			file := player.playlist.Next()
			if file != "" {
				if err := player.Load(file); err != nil {
					fyne.Do(func() { dialog.ShowError(err, w) })
				} else {
					player.Play()
					fyne.Do(func() {
						syncing = true
						selectedPlaylistIndex = player.playlist.Index()
						playlistList.Refresh()
						syncing = false
					})
				}
			}
		}()
	})
	delBtn := widget.NewButtonWithIcon("Del", theme.DeleteIcon(), func() {
		if selectedPlaylistIndex < 0 {
			return
		}
		player.playlist.RemoveAt(selectedPlaylistIndex)
		if player.playlist.Len() == 0 {
			selectedPlaylistIndex = -1
			player.Stop()
		} else {
			if selectedPlaylistIndex >= player.playlist.Len() {
				selectedPlaylistIndex = player.playlist.Len() - 1
			}
			player.playlist.SetIndex(selectedPlaylistIndex)
		}
		refreshPlaylist()
	})

	infoBtn := widget.NewButtonWithIcon("", theme.InfoIcon(), func() {
		files := player.playlist.Files()
		if selectedPlaylistIndex >= 0 && selectedPlaylistIndex < len(files) {
			player.ShowFileInfo(files[selectedPlaylistIndex], w)
		} else {
			player.ShowInfo(w)
		}
	})

	controls := container.NewHBox(prevBtn, playBtn, pauseBtn, stopBtn, nextBtn, infoBtn, openBtn, delBtn)

	currentTimeLabel := widget.NewLabel("00:00")
	totalTimeLabel := widget.NewLabel("00:00")
	player.currentTimeLabel = currentTimeLabel
	player.totalTimeLabel = totalTimeLabel
	timeContainer := container.NewHBox(currentTimeLabel, widget.NewLabel(" / "), totalTimeLabel)

	progressBar := widget.NewSlider(0, 1)
	progressBar.Step = 0.001
	player.progressBar = progressBar
	progressBar.OnChanged = func(value float64) { player.Seek(value) }

	volumeSlider := widget.NewSlider(0, 100)
	volumeSlider.Value = 50
	volumeSlider.OnChanged = func(value float64) {
		player.SetVolume(value / 100.0)
		fyne.Do(func() { volumeValueLabel.SetText(fmt.Sprintf("%.0f%%", value)) })
	}
	player.volumeSlider = volumeSlider

	playlistList.OnSelected = func(id int) {
		if syncing {
			return
		}
		files := player.playlist.Files()
		if id < 0 || id >= len(files) {
			playlistList.Unselect(id)
			return
		}
		now := time.Now()
		if id == lastTapIndex && now.Sub(lastTapTime) < 300*time.Millisecond {
			// double click -> play
			player.playlist.SetIndex(id)
			selectedPlaylistIndex = id
			playlistList.Refresh()
			go func() {
				player.Stop()
				if err := player.Load(files[id]); err != nil {
					fyne.Do(func() { dialog.ShowError(err, w) })
				} else {
					player.Play()
				}
			}()
		} else {
			// single click -> just select
			selectedPlaylistIndex = id
			player.playlist.SetIndex(id)
			playlistList.Refresh()
		}
		lastTapIndex = id
		lastTapTime = now
		playlistList.Unselect(id)
	}

	player.onTrackEnd = func() {
		file := player.playlist.Next()
		if file != "" {
			if err := player.Load(file); err != nil {
				fyne.Do(func() { dialog.ShowError(err, w) })
			} else {
				player.Play()
				fyne.Do(func() {
					syncing = true
					selectedPlaylistIndex = player.playlist.Index()
					playlistList.Refresh()
					syncing = false
				})
			}
		}
	}

	if playlist.Len() > 0 {
		file := playlist.Next()
		go func() {
			if err := player.Load(file); err != nil {
				fyne.Do(func() { dialog.ShowError(err, w) })
			} else {
				player.Play()
				fyne.Do(func() {
					syncing = true
					selectedPlaylistIndex = player.playlist.Index()
					playlistList.Refresh()
					syncing = false
				})
			}

		}()
	}

	// Visualizer animation: реальный спектр из потока аудио (FFT)
	go func() {
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			player.mu.Lock()
			analyzer := player.analyzer
			playing := player.isPlaying
			player.mu.Unlock()
			if playing && analyzer != nil {
				bars := make([]float64, len(specData.bars))
				analyzer.Update(bars)
				specData.SetBars(bars)
			} else {
				specData.Decay()
			}
			fyne.Do(func() { canvas.Refresh(visualizer) })
		}
	}()

	w.Canvas().AddShortcut(deleteShortcut{}, func(_ fyne.Shortcut) {
		if selectedPlaylistIndex < 0 {
			return
		}
		player.playlist.RemoveAt(selectedPlaylistIndex)
		if player.playlist.Len() == 0 {
			selectedPlaylistIndex = -1
			player.Stop()
		} else {
			if selectedPlaylistIndex >= player.playlist.Len() {
				selectedPlaylistIndex = player.playlist.Len() - 1
			}
			player.playlist.SetIndex(selectedPlaylistIndex)
		}
		refreshPlaylist()
	})

	content := container.NewVBox(
		container.NewHBox(trackLabel, container.NewHBox(statusLabel)),
		visualizer,
		controls,
		widget.NewLabel("Progress:"),
		progressBar,
		container.NewCenter(timeContainer),
		container.NewBorder(nil, nil, widget.NewLabel("Volume:"), volumeValueLabel, volumeSlider),
		widget.NewLabel("Playlist:"),
		playlistScroll,
	)
	w.SetContent(content)
	w.ShowAndRun()

	debugPrintf("Audio Player shutdown")
}
