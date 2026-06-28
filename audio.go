package main

import (
	"bytes"
	_ "embed"
	"io"
	"log"

	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
)

// Audio is split off from main.go because the //go:embed block plus the SE/BGM
// plumbing is large, and the rest of the file is already dense with gameplay
// logic. Same pattern as debug_mode_*.go: pull self-contained subsystems out.

const audioSampleRate = 44100

var (
	//go:embed assets/bgm.mp3
	bgmMP3 []byte
	//go:embed assets/slash.mp3
	slashMP3 []byte
	//go:embed assets/slash_short.mp3
	slashShortMP3 []byte
	//go:embed assets/seal.mp3
	sealMP3 []byte
	//go:embed assets/enemy_down.mp3
	enemyDownMP3 []byte
	//go:embed assets/erode.mp3
	erodeMP3 []byte
	//go:embed assets/clear.mp3
	clearMP3 []byte
	//go:embed assets/gameover.mp3
	gameoverMP3 []byte
	//go:embed assets/confirm.mp3
	confirmMP3 []byte
)

type sfxID int

const (
	sfxSlash sfxID = iota
	sfxSlashShort
	sfxSeal
	sfxEnemyDown
	sfxErode
	sfxClear
	sfxGameOver
	sfxConfirm
)

type soundboard struct {
	ctx       *audio.Context
	pcm       [8][]byte
	volume    [8]float64
	bgmPlayer *audio.Player
	// active holds one-shot SE players that are still playing. Without this
	// the GC can reclaim a *audio.Player whose only reference was a local
	// variable in playSFX — the result is silence (or a clipped first note)
	// even though Play() was called. Most visible on the very first SE after
	// page load when the audio backend's caches are still cold.
	active []*audio.Player
}

var sb *soundboard

// initAudio decodes every MP3 to raw PCM up front so that play-time only has
// to wrap a []byte in a Player — no per-shot decoder allocation, no GC stall
// mid-slash. BGM stays as a streaming InfiniteLoop because decoding ~1 MB into
// PCM (≈10 MB) would bloat memory for no benefit. Returns nil-tolerant: a
// decode failure logs and disables that one sound rather than killing boot.
func initAudio() {
	if sb != nil {
		return
	}
	ctx := audio.NewContext(audioSampleRate)
	sb = &soundboard{ctx: ctx}

	specs := []struct {
		id     sfxID
		data   []byte
		volume float64
	}{
		{sfxSlash, slashMP3, 0.35},
		{sfxSlashShort, slashShortMP3, 0.25},
		{sfxSeal, sealMP3, 0.45},
		{sfxEnemyDown, enemyDownMP3, 0.30},
		{sfxErode, erodeMP3, 0.18},
		{sfxClear, clearMP3, 0.45},
		{sfxGameOver, gameoverMP3, 0.45},
		{sfxConfirm, confirmMP3, 0.35},
	}
	for _, s := range specs {
		stream, err := mp3.DecodeWithSampleRate(audioSampleRate, bytes.NewReader(s.data))
		if err != nil {
			log.Printf("audio: decode sfx %d failed: %v", s.id, err)
			continue
		}
		pcm, err := io.ReadAll(stream)
		if err != nil {
			log.Printf("audio: read sfx %d failed: %v", s.id, err)
			continue
		}
		sb.pcm[s.id] = pcm
		sb.volume[s.id] = s.volume
	}

	bgmStream, err := mp3.DecodeWithSampleRate(audioSampleRate, bytes.NewReader(bgmMP3))
	if err != nil {
		log.Printf("audio: decode bgm failed: %v", err)
		return
	}
	loop := audio.NewInfiniteLoop(bgmStream, bgmStream.Length())
	p, err := ctx.NewPlayer(loop)
	if err != nil {
		log.Printf("audio: bgm player failed: %v", err)
		return
	}
	p.SetVolume(0.18)
	sb.bgmPlayer = p
}

// playSFX spawns a one-shot player from the cached PCM. NewPlayerFromBytes is
// cheap (no decoding), so spamming this — e.g. one per slash, per enemy down,
// per Feeding rising edge — is fine.
func playSFX(id sfxID) {
	if sb == nil {
		return
	}
	pcm := sb.pcm[id]
	if pcm == nil {
		return
	}
	p := sb.ctx.NewPlayerFromBytes(pcm)
	p.SetVolume(sb.volume[id])
	p.Play()

	// Reap finished players, then retain the new one. Sweep is O(n) but n is
	// the number of currently-playing one-shots, which stays small (a few
	// dozen at most during a frantic seal). Without retention the player gets
	// GC'd and the sound cuts off — see soundboard.active.
	out := sb.active[:0]
	for _, q := range sb.active {
		if q.IsPlaying() {
			out = append(out, q)
		}
	}
	sb.active = append(out, p)
}

// playBGM is idempotent: calling it every frame while StatePlaying is fine.
// Without the IsPlaying gate, Rewind would reset the playhead constantly and
// the BGM would never advance past the first samples.
func playBGM() {
	if sb == nil || sb.bgmPlayer == nil {
		return
	}
	if sb.bgmPlayer.IsPlaying() {
		return
	}
	if err := sb.bgmPlayer.Rewind(); err != nil {
		log.Printf("audio: bgm rewind: %v", err)
		return
	}
	sb.bgmPlayer.Play()
}

func stopBGM() {
	if sb == nil || sb.bgmPlayer == nil {
		return
	}
	sb.bgmPlayer.Pause()
}
