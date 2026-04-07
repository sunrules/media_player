# Audio Player

A modern audio player for Windows with support for multiple formats and console mode.

## Features

### Supported Formats
- **MP3** - MPEG Audio Layer 3
- **FLAC** - Free Lossless Audio Codec (lossless compression)
- **OGG** - Ogg Vorbis
- **WAV** - Waveform Audio File Format

### Operating Modes
- **GUI Mode** - Graphical interface with buttons and sliders
- **Console Mode** - Keyboard controls

### Playback Functions
- ▶ Play audio files
- ⏸ Pause
- ⏹ Stop
- Seek using progress slider or arrow keys
- Volume control (logarithmic scale)
- Automatic track duration detection
- Playlist with folder and multiple file support

### File Information
The "Info" button displays detailed information about the loaded file:
- File name
- Format
- File size
- Sample rate
- Number of channels (mono/stereo)
- Duration
- Number of samples

### User Interface (GUI Mode)
- Modern GUI based on Fyne
- Current time and total duration display
- Progress bar with seek capability
- Intuitive controls

## Usage

### Launch without parameters (GUI Mode)
```bash
.\mplayer.exe
```

### Console Mode
```bash
.\mplayer.exe -console song.mp3
.\mplayer.exe -console C:\Music
.\mplayer.exe -console song1.mp3 song2.flac
```

### Help
```bash
.\mplayer.exe -help
```

### Debug Mode
```bash
.\mplayer.exe -debug
```
When launched with the `-debug` flag, a `debug.log` file is created with detailed player operation logs.

## Hotkeys (Console Mode)

| Key | Action |
|---------|----------|
| `p` | Pause / Resume |
| `s` | Stop |
| `n` | Next track |
| `r` | Previous track |
| `+` | Increase volume |
| `-` | Decrease volume |
| `←` | Seek backward (5 seconds) |
| `→` | Seek forward (5 seconds) |
| `x` / `q` / `Esc` | Exit |
| `h` | Command help |

## Controls (GUI Mode)

1. Click "Open Audio File" to select a file
2. Use control buttons:
   - **▶ Play** - start playback
   - **⏸ Pause** - pause playback
   - **⏹ Stop** - stop and rewind to beginning
   - **ℹ Info** - show file information
3. Adjust volume using the slider
4. Seek through the track using the progress slider

## Technical Details

### Libraries Used
- **beep** - Audio playback library with robust buffering
- **Fyne** - Cross-platform GUI framework
- **keyboard** - Keyboard event handling library

### Architecture Advantages
- Using beep library ensures smooth playback without interruptions
- 100ms buffer for stable audio
- Logarithmic volume scale for natural control
- Thread-safe design using sync.Mutex
- Playlist support with cyclic track switching

## Building

```bash
go build -o mplayer.exe -ldflags "-s -w"
```

## System Requirements

- Windows 10/11
- Audio playback device

## Licenses

Used libraries are distributed under their respective licenses:
- beep - MIT License
- Fyne - BSD 3-Clause License
- keyboard - MIT License
