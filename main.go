// go build -o mplayer.exe -ldflags "-s -w -H=windowsgui" 2>&1
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
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

	// State
	isPlaying      bool
	volume         float64
	volumeStreamer *effects.Volume

	// UI elements
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
	filePath     string
	fileFormat   string
	fileSize     int64

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
		streamer = s
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

	// Get file size
	fileInfo, err := file.Stat()
	if err == nil {
		p.fileSize = fileInfo.Size()
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

	// Create volume effect
	p.volumeStreamer = &effects.Volume{
		Streamer: p.ctrl,
		Base:     2,
		Volume:   -5 + p.volume*5,
		Silent:   p.volume <= 0,
	}

	// Play through speaker
	speaker.Play(p.volumeStreamer)

	p.isPlaying = true
	p.startTime = time.Now()

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

// Seek seeks to position (0.0 - 1.0)
func (p *Player) Seek(pos float64) {
	debugPrintf("Seeking to position: %.2f", pos)
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streamer == nil || pos < 0 || pos > 1 {
		debugPrintf("Invalid parameters for seek")
		return
	}

	wasPlaying := p.isPlaying

	// Stop current playback
	if p.ctrl != nil {
		p.ctrl.Paused = true
	}

	// Calculate target position in samples
	targetSample := int(pos * float64(p.totalSamples))
	debugPrintf("Target position: %d samples out of %d", targetSample, p.totalSamples)

	// Seek
	err := p.streamer.Seek(targetSample)
	if err != nil {
		debugPrintErr(err, "Seek error")
		return
	}

	// Update elapsedAtPause
	duration := p.sampleRate.D(p.totalSamples).Seconds()
	if duration > 0 {
		p.elapsedAtPause = time.Duration(pos * duration * float64(time.Second))
	} else {
		p.elapsedAtPause = 0
	}

	currentSeconds := pos * duration
	debugPrintf("Current time: %.2f seconds", currentSeconds)

	// Update UI
	if p.progressBar != nil {
		fyne.Do(func() {
			p.progressBar.Value = pos
			p.progressBar.Refresh()
			p.updateTimeDisplay(currentSeconds, duration)
		})
	} else {
		p.updateTimeDisplay(currentSeconds, duration)
	}

	// If was playing - restart from new position
	if wasPlaying {
		// Recreate streamer -> ctrl -> volume chain for correct operation
		p.ctrl = &beep.Ctrl{Streamer: p.streamer, Paused: false}
		p.volumeStreamer = &effects.Volume{
			Streamer: p.ctrl,
			Base:     2,
			Volume:   -5 + p.volume*5,
			Silent:   p.volume <= 0,
		}
		speaker.Play(p.volumeStreamer)
		p.isPlaying = true
		p.startTime = time.Now()
	}
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

	// GUI mode
	a := app.NewWithID("com.example.audioplayer")
	w := a.NewWindow("Audio Player")
	w.Resize(fyne.NewSize(450, 220))

	player := NewPlayer(w)
	player.playlist = playlist

	// If there are files in the playlist, load the first one
	if playlist.Len() > 0 {
		file := playlist.Next()
		go func() {
			if err := player.Load(file); err != nil {
				fmt.Printf("Load error: %v\n", err)
			} else {
				player.Play()
			}
		}()
	}

	openBtn := widget.NewButton("Open Audio File", func() {
		debugPrintf("Open Audio File button clicked")
		dialog.ShowFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil || r == nil {
				debugPrintErr(err, "File open canceled or error")
				return
			}
			defer r.Close()

			path := r.URI().Path()
			debugPrintf("Selected file: %s", path)

			loadingDialog := dialog.NewInformation("Loading", "Loading audio file...", w)
			loadingDialog.Show()

			go func() {
				if err := player.Load(path); err != nil {
					fyne.Do(func() {
						loadingDialog.Hide()
						dialog.ShowError(err, w)
					})
				} else {
					fyne.Do(func() {
						loadingDialog.Hide()
						player.Play()
					})
				}
			}()
		}, w)
	})

	volumeLabel := widget.NewLabel("Volume:")
	volumeSlider := widget.NewSlider(0, 100)
	volumeSlider.Value = 50
	volumeSlider.OnChanged = func(value float64) {
		player.SetVolume(value / 100.0)
	}
	player.volumeSlider = volumeSlider

	currentTimeLabel := widget.NewLabel("00:00")
	totalTimeLabel := widget.NewLabel("00:00")
	player.currentTimeLabel = currentTimeLabel
	player.totalTimeLabel = totalTimeLabel

	timeContainer := container.NewHBox(
		currentTimeLabel,
		widget.NewLabel(" / "),
		totalTimeLabel,
	)

	progressBar := widget.NewSlider(0, 1)
	progressBar.Step = 0.001

	player.progressBar = progressBar

	progressBar.OnChanged = func(value float64) {
		player.Seek(value)
	}

	playBtn := widget.NewButton("▶ Play", func() {
		player.Play()
	})

	pauseBtn := widget.NewButton("⏸ Pause", func() {
		player.Pause()
	})

	stopBtn := widget.NewButton("⏹ Stop", func() {
		player.Stop()
	})

	infoBtn := widget.NewButton("ℹ Info", func() {
		player.ShowInfo(w)
	})

	controls := container.NewHBox(
		infoBtn,
		playBtn,
		pauseBtn,
		stopBtn,
	)

	content := container.NewVBox(
		openBtn,
		container.NewCenter(controls),
		volumeLabel,
		volumeSlider,
		widget.NewLabel("Progress:"),
		progressBar,
		container.NewCenter(timeContainer),
	)

	w.SetContent(content)
	w.ShowAndRun()

	debugPrintf("Audio Player shutdown")
}
