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

// Playlist управляет списком воспроизведения
type Playlist struct {
	files []string
	index int
	mu    sync.Mutex
}

// NewPlaylist создает новый плейлист
func NewPlaylist() *Playlist {
	return &Playlist{
		files: make([]string, 0),
		index: -1,
	}
}

// AddFile добавляет файл в плейлист
func (pl *Playlist) AddFile(path string) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.files = append(pl.files, path)
}

// AddDirectory добавляет все аудиофайлы из директории
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

// Current возвращает текущий файл
func (pl *Playlist) Current() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.index < 0 || pl.index >= len(pl.files) {
		return ""
	}
	return pl.files[pl.index]
}

// Next переходит к следующему файлу
func (pl *Playlist) Next() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if len(pl.files) == 0 {
		return ""
	}
	pl.index = (pl.index + 1) % len(pl.files)
	return pl.files[pl.index]
}

// Prev переходит к предыдущему файлу
func (pl *Playlist) Prev() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if len(pl.files) == 0 {
		return ""
	}
	pl.index = (pl.index - 1 + len(pl.files)) % len(pl.files)
	return pl.files[pl.index]
}

// Len возвращает количество файлов
func (pl *Playlist) Len() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return len(pl.files)
}

// Index возвращает текущий индекс
func (pl *Playlist) Index() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.index
}

// SetIndex устанавливает текущий индекс
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

// Player - медиаплеер с использованием beep
type Player struct {
	mu sync.Mutex

	// Аудио
	streamer      beep.StreamSeeker
	ctrl          *beep.Ctrl
	format        beep.Format
	speakerInited bool

	// Состояние
	isPlaying      bool
	volume         float64
	volumeStreamer *effects.Volume

	// UI элементы
	volumeSlider     *widget.Slider
	progressBar      *widget.Slider
	currentTimeLabel *widget.Label
	totalTimeLabel   *widget.Label
	window           fyne.Window

	// Время и прогресс
	totalSamples   int
	sampleRate     beep.SampleRate
	startTime      time.Time
	elapsedAtPause time.Duration
	stopProgress   chan struct{}
	progressActive bool

	// Файл
	filePath     string
	fileFormat   string
	fileSize     int64

	// Плейлист
	playlist *Playlist

	// Callback при окончании трека
	onTrackEnd func()
}

// NewPlayer создает новый плеер
func NewPlayer(window fyne.Window) *Player {
	debugPrintf("Создание нового плеера")
	return &Player{
		volume: 0.5,
		window: window,
	}
}

// SetVolume устанавливает громкость
func (p *Player) SetVolume(volume float64) {
	debugPrintf("Установка громкости: %.2f", volume)
	p.mu.Lock()
	defer p.mu.Unlock()

	p.volume = volume
	if p.volumeStreamer != nil {
		// Преобразуем линейную громкость в децибелы для лучшего контроля
		if volume <= 0 {
			p.volumeStreamer.Silent = true
		} else {
			p.volumeStreamer.Silent = false
			// Логарифмическая шкала громкости
			p.volumeStreamer.Volume = -5 + volume*5
		}
	}
}

// formatTime форматирует время в MM:SS
func (p *Player) formatTime(seconds float64) string {
	total := int(seconds)
	minutes := total / 60
	secondsRemaining := total % 60
	return fmt.Sprintf("%02d:%02d", minutes, secondsRemaining)
}

// updateTimeDisplay обновляет отображение времени
func (p *Player) updateTimeDisplay(currentSeconds, totalSeconds float64) {
	if p.currentTimeLabel != nil && p.totalTimeLabel != nil {
		fyne.Do(func() {
			p.currentTimeLabel.SetText(p.formatTime(currentSeconds))
			p.totalTimeLabel.SetText(p.formatTime(totalSeconds))
		})
	}
}

// Load загружает аудиофайл
func (p *Player) Load(path string) error {
	debugPrintf("Загрузка файла: %s", path)
	p.mu.Lock()
	defer p.mu.Unlock()

	// Останавливаем текущее воспроизведение
	p.stopPlayback()

	// Открываем файл
	file, err := os.Open(path)
	if err != nil {
		debugPrintErr(err, "Ошибка открытия файла")
		return fmt.Errorf("не удалось открыть файл: %w", err)
	}

	// Определяем формат и создаем декодер
	ext := getFileExtension(path)
	var streamer beep.StreamSeeker
	var format beep.Format

	switch ext {
	case "mp3":
		s, f, err := mp3.Decode(file)
		if err != nil {
			file.Close()
			debugPrintErr(err, "Ошибка декодирования MP3")
			return fmt.Errorf("ошибка декодирования MP3: %w", err)
		}
		streamer = s
		format = f

	case "flac":
		s, f, err := flac.Decode(file)
		if err != nil {
			file.Close()
			debugPrintErr(err, "Ошибка декодирования FLAC")
			return fmt.Errorf("ошибка декодирования FLAC: %w", err)
		}
		streamer = s
		format = f

	case "ogg":
		s, f, err := vorbis.Decode(file)
		if err != nil {
			file.Close()
			debugPrintErr(err, "Ошибка декодирования OGG")
			return fmt.Errorf("ошибка декодирования OGG: %w", err)
		}
		streamer = s
		format = f

	case "wav":
		s, f, err := wav.Decode(file)
		if err != nil {
			file.Close()
			debugPrintErr(err, "Ошибка декодирования WAV")
			return fmt.Errorf("ошибка декодирования WAV: %w", err)
		}
		streamer = s
		format = f

	default:
		file.Close()
		return fmt.Errorf("неподдерживаемый формат: %s", ext)
	}

	// Инициализируем speaker если нужно или если изменился sample rate
	if !p.speakerInited || p.sampleRate != format.SampleRate {
		if p.speakerInited {
			speaker.Close()
		}

		// Инициализируем speaker с буфером 100ms для плавного воспроизведения
		err := speaker.Init(format.SampleRate, format.SampleRate.N(time.Millisecond*100))
		if err != nil {
			debugPrintErr(err, "Ошибка инициализации speaker")
			return fmt.Errorf("ошибка инициализации аудио: %w", err)
		}
		p.speakerInited = true
		p.sampleRate = format.SampleRate
	}

	// Получаем размер файла
	fileInfo, err := file.Stat()
	if err == nil {
		p.fileSize = fileInfo.Size()
	}

	// Сохраняем состояние
	p.streamer = streamer
	p.format = format
	p.filePath = path
	p.fileFormat = ext
	p.totalSamples = streamer.Len()

	// Вычисляем длительность
	duration := format.SampleRate.D(p.totalSamples).Seconds()
	debugPrintf("Длительность трека: %.2f секунд, формат: %s, сэмплов: %d, частота: %d Hz",
		duration, p.fileFormat, p.totalSamples, format.SampleRate)

	// Сбрасываем состояние
	p.isPlaying = false
	p.elapsedAtPause = 0

	// Обновляем UI
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

// getFileExtension возвращает расширение файла в нижнем регистре
func getFileExtension(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return strings.ToLower(path[i+1:])
		}
	}
	return ""
}

// stopPlayback останавливает воспроизведение
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

// Pause ставит воспроизведение на паузу
func (p *Player) Pause() {
	debugPrintf("Пауза")
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.isPlaying || p.ctrl == nil {
		return
	}

	// Запоминаем прошедшее время
	if p.isPlaying {
		p.elapsedAtPause += time.Since(p.startTime)
	}

	p.ctrl.Paused = true
	p.isPlaying = false

	// Останавливаем обновление прогресса
	if p.stopProgress != nil {
		select {
		case p.stopProgress <- struct{}{}:
		default:
		}
		p.progressActive = false
	}
}

// Stop останавливает воспроизведение
func (p *Player) Stop() {
	debugPrintf("Остановка")
	p.mu.Lock()
	defer p.mu.Unlock()

	p.stopPlayback()

	// Перематываем в начало
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

// Play начинает воспроизведение
func (p *Player) Play() {
	debugPrintf("Воспроизведение")
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streamer == nil {
		debugPrintf("Декодер не загружен, воспроизведение невозможно")
		return
	}

	if p.isPlaying {
		debugPrintf("Воспроизведение уже активно")
		return
	}

	// Создаем контроллер
	p.ctrl = &beep.Ctrl{Streamer: p.streamer, Paused: false}

	// Создаем эффект громкости
	p.volumeStreamer = &effects.Volume{
		Streamer: p.ctrl,
		Base:     2,
		Volume:   -5 + p.volume*5,
		Silent:   p.volume <= 0,
	}

	// Воспроизводим через speaker
	speaker.Play(p.volumeStreamer)

	p.isPlaying = true
	p.startTime = time.Now()

	// Запускаем обновление прогресса
	if !p.progressActive {
		p.stopProgress = make(chan struct{}, 1)
		go p.updateProgress()
		p.progressActive = true
	}
}

// updateProgress обновляет прогресс воспроизведения
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

			// Вычисляем текущую позицию
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
				
				// Трек закончился - вызываем callback если есть
				if p.onTrackEnd != nil {
					p.onTrackEnd()
				} else {
					p.Stop()
				}
				continue
			}

			// Обновляем UI
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

// ShowInfo показывает информацию о загруженном файле
func (p *Player) ShowInfo(parent fyne.Window) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streamer == nil {
		dialog.ShowInformation("Информация", "Файл не загружен", parent)
		return
	}

	duration := p.sampleRate.D(p.totalSamples).Seconds()
	
	// Форматируем размер файла
	var sizeStr string
	if p.fileSize < 1024 {
		sizeStr = fmt.Sprintf("%d байт", p.fileSize)
	} else if p.fileSize < 1024*1024 {
		sizeStr = fmt.Sprintf("%.2f КБ", float64(p.fileSize)/1024)
	} else {
		sizeStr = fmt.Sprintf("%.2f МБ", float64(p.fileSize)/(1024*1024))
	}

	// Получаем имя файла из пути
	fileName := p.filePath
	for i := len(p.filePath) - 1; i >= 0; i-- {
		if p.filePath[i] == '\\' || p.filePath[i] == '/' {
			fileName = p.filePath[i+1:]
			break
		}
	}

	info := fmt.Sprintf(`Информация о файле:

Имя файла: %s
Формат: %s
Размер: %s

Частота дискретизации: %d Гц
Количество каналов: %d
Длительность: %s

Всего сэмплов: %d`,
		fileName,
		strings.ToUpper(p.fileFormat),
		sizeStr,
		p.sampleRate,
		p.format.NumChannels,
		p.formatTime(duration),
		p.totalSamples,
	)

	dialog.ShowInformation("Информация о медиафайле", info, parent)
}

// Seek перематывает на позицию (0.0 - 1.0)
func (p *Player) Seek(pos float64) {
	debugPrintf("Перемотка до позиции: %.2f", pos)
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streamer == nil || pos < 0 || pos > 1 {
		debugPrintf("Некорректные параметры для перемотки")
		return
	}

	wasPlaying := p.isPlaying

	// Останавливаем текущее воспроизведение
	if p.ctrl != nil {
		p.ctrl.Paused = true
	}

	// Вычисляем целевую позицию в сэмплах
	targetSample := int(pos * float64(p.totalSamples))
	debugPrintf("Целевая позиция: %d сэмплов из %d", targetSample, p.totalSamples)

	// Перематываем
	err := p.streamer.Seek(targetSample)
	if err != nil {
		debugPrintErr(err, "Ошибка перемотки")
		return
	}

	// Обновляем elapsedAtPause
	duration := p.sampleRate.D(p.totalSamples).Seconds()
	if duration > 0 {
		p.elapsedAtPause = time.Duration(pos * duration * float64(time.Second))
	} else {
		p.elapsedAtPause = 0
	}

	currentSeconds := pos * duration
	debugPrintf("Текущее время: %.2f секунд", currentSeconds)

	// Обновляем UI
	if p.progressBar != nil {
		fyne.Do(func() {
			p.progressBar.Value = pos
			p.progressBar.Refresh()
			p.updateTimeDisplay(currentSeconds, duration)
		})
	} else {
		p.updateTimeDisplay(currentSeconds, duration)
	}

	// Если было воспроизведение - перезапускаем с новой позиции
	if wasPlaying {
		// Пересоздаем цепочку streamer -> ctrl -> volume для корректной работы
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
	fmt.Println(`Audio Player - консольный медиаплеер

ИСПОЛЬЗОВАНИЕ:
  mplayer.exe [опции] [файлы или папки]

ОПЦИИ:
  -console    Запуск в консольном режиме
  -debug      Включить debug режим с логированием в debug.log
  -help       Показать эту справку

ПРИМЕРЫ:
  mplayer.exe song.mp3                    # Воспроизвести один файл
  mplayer.exe song1.mp3 song2.flac        # Воспроизвести несколько файлов
  mplayer.exe C:\Music                    # Воспроизвести все файлы из папки
  mplayer.exe -console C:\Music           # Консольный режим с папкой

ГОРЯЧИЕ КЛАВИШИ (консольный режим):
  p       Пауза / Продолжить
  s       Стоп
  n       Следующий трек
  r       Предыдущий трек
  +       Увеличить громкость
  -       Уменьшить громкость
  ←       Перемотка назад (5 секунд)
  →       Перемотка вперед (5 секунд)
  x       Выход
  q       Выход
  Esc     Выход

ПОДДЕРЖИВАЕМЫЕ ФОРМАТЫ:
  MP3, FLAC, OGG, WAV`)
}

func runConsoleMode(playlist *Playlist) {
	fmt.Println("=== Audio Player - Консольный режим ===")
	fmt.Printf("Плейлист: %d файл(ов)\n", playlist.Len())
	fmt.Println("Нажмите 'h' для справки по командам")
	fmt.Println("=========================================")

	player := NewPlayer(nil)
	player.playlist = playlist

	// Функция для загрузки и воспроизведения следующего трека
	playNextTrack := func() {
		file := playlist.Next()
		if file != "" {
			if err := player.Load(file); err != nil {
				fmt.Printf("Ошибка загрузки: %v\n", err)
			} else {
				fmt.Printf("▶ Следующий: %s\n", filepath.Base(file))
				player.Play()
			}
		} else {
			fmt.Println("Плейлист завершен")
		}
	}

	// Устанавливаем callback для автоматического переключения
	player.onTrackEnd = playNextTrack

	// Загружаем первый трек
	if playlist.Len() > 0 {
		file := playlist.Next()
		if err := player.Load(file); err != nil {
			fmt.Printf("Ошибка загрузки: %v\n", err)
		} else {
			fmt.Printf("Загружен: %s\n", filepath.Base(file))
			player.Play()
		}
	}

	// Обработка клавиш
	if err := keyboard.Open(); err != nil {
		fmt.Printf("Ошибка инициализации клавиатуры: %v\n", err)
		fmt.Println("Нажмите Enter для выхода...")
		fmt.Scanln()
		return
	}
	defer keyboard.Close()

	fmt.Println("Плеер запущен. Нажмите 'h' для справки.")

	for {
		char, key, err := keyboard.GetKey()
		if err != nil {
			fmt.Printf("Ошибка чтения клавиши: %v\n", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		switch {
		case key == keyboard.KeyEsc || char == 'x' || char == 'q':
			fmt.Println("Выход...")
			player.Stop()
			return

		case char == 'p':
			player.mu.Lock()
			wasPlaying := player.isPlaying
			player.mu.Unlock()
			
			if wasPlaying {
				player.Pause()
				fmt.Println("⏸ Пауза")
			} else {
				player.Play()
				fmt.Println("▶ Продолжить")
			}

		case char == 's':
			player.Stop()
			fmt.Println("⏹ Стоп")

		case char == 'n':
			player.Stop()
			file := playlist.Next()
			if file != "" {
				if err := player.Load(file); err != nil {
					fmt.Printf("Ошибка загрузки: %v\n", err)
				} else {
					fmt.Printf("▶ Следующий: %s\n", filepath.Base(file))
					player.Play()
				}
			} else {
				fmt.Println("Плейлист пуст")
			}

		case char == 'r':
			player.Stop()
			file := playlist.Prev()
			if file != "" {
				if err := player.Load(file); err != nil {
					fmt.Printf("Ошибка загрузки: %v\n", err)
				} else {
					fmt.Printf("▶ Предыдущий: %s\n", filepath.Base(file))
					player.Play()
				}
			} else {
				fmt.Println("Плейлист пуст")
			}

		case char == '+':
			player.mu.Lock()
			newVol := player.volume + 0.1
			if newVol > 1.0 {
				newVol = 1.0
			}
			player.mu.Unlock()
			player.SetVolume(newVol)
			fmt.Printf("🔊 Громкость: %d%%\n", int(newVol*100))

		case char == '-':
			player.mu.Lock()
			newVol := player.volume - 0.1
			if newVol < 0 {
				newVol = 0
			}
			player.mu.Unlock()
			player.SetVolume(newVol)
			fmt.Printf("🔉 Громкость: %d%%\n", int(newVol*100))

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
				fmt.Printf("⏪ Перемотка назад\n")
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
				fmt.Printf("⏩ Перемотка вперед\n")
			} else {
				player.mu.Unlock()
			}

		case char == 'h':
			fmt.Println(`
Команды:
  p - Пауза/Продолжить
  s - Стоп
  n - Следующий трек
  r - Предыдущий трек
  + - Увеличить громкость
  - - Уменьшить громкость
  ← - Перемотка назад (5 сек)
  → - Перемотка вперед (5 сек)
  x/q/Esc - Выход`)
		}
	}
}

func main() {
	flag.Parse()

	// Обработка справки до инициализации debug
	if helpFlag {
		showHelp()
		return
	}

	if debugFlag {
		var err error
		debugLogger, err = os.OpenFile("debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			fmt.Printf("Ошибка открытия debug.log: %v\n", err)
			debugFlag = false
		} else {
			defer debugLogger.Close()
		}
	}
	debugPrintf("Запуск Audio Player (debug: %v, console: %v)", debugFlag, consoleFlag)

	// Создаем плейлист из аргументов командной строки
	playlist := NewPlaylist()
	args := flag.Args()

	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Printf("Ошибка: %v\n", err)
			continue
		}
		if info.IsDir() {
			if err := playlist.AddDirectory(arg); err != nil {
				fmt.Printf("Ошибка чтения директории: %v\n", err)
			}
		} else {
			ext := strings.ToLower(filepath.Ext(arg))
			if ext == ".mp3" || ext == ".flac" || ext == ".ogg" || ext == ".wav" {
				playlist.AddFile(arg)
			}
		}
	}

	// Консольный режим
	if consoleFlag {
		if playlist.Len() == 0 {
			fmt.Println("Ошибка: не указаны файлы для воспроизведения")
			fmt.Println("Используйте: mplayer.exe -help для справки")
			return
		}
		runConsoleMode(playlist)
		return
	}

	// GUI режим
	a := app.NewWithID("com.example.audioplayer")
	w := a.NewWindow("Audio Player")
	w.Resize(fyne.NewSize(450, 220))

	player := NewPlayer(w)
	player.playlist = playlist

	// Если есть файлы в плейлисте, загружаем первый
	if playlist.Len() > 0 {
		file := playlist.Next()
		go func() {
			if err := player.Load(file); err != nil {
				fmt.Printf("Ошибка загрузки: %v\n", err)
			} else {
				player.Play()
			}
		}()
	}

	openBtn := widget.NewButton("Открыть аудиофайл", func() {
		debugPrintf("Нажатие на кнопку 'Открыть аудиофайл'")
		dialog.ShowFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil || r == nil {
				debugPrintErr(err, "Открытие файла отменено или ошибка")
				return
			}
			defer r.Close()

			path := r.URI().Path()
			debugPrintf("Выбран файл: %s", path)

			loadingDialog := dialog.NewInformation("Загрузка", "Загрузка аудиофайла...", w)
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

	volumeLabel := widget.NewLabel("Громкость:")
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

	playBtn := widget.NewButton("▶ Воспроизвести", func() {
		player.Play()
	})

	pauseBtn := widget.NewButton("⏸ Пауза", func() {
		player.Pause()
	})

	stopBtn := widget.NewButton("⏹ Остановить", func() {
		player.Stop()
	})

	infoBtn := widget.NewButton("ℹ Инфо", func() {
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
		widget.NewLabel("Прогресс:"),
		progressBar,
		container.NewCenter(timeContainer),
	)

	w.SetContent(content)
	w.ShowAndRun()

	debugPrintf("Завершение работы Audio Player")
}
