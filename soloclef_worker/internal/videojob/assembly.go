package videojob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path"
	"strconv"
	"strings"
)

// Assembling a video from stills instead of every frame
// (app/deliverables/video/stills_assembly): Remotion renders each scene's
// entrance and the one still that carries the rest of it; ffmpeg holds the
// still for the frames the narration needs and joins the pieces.
//
// Two rules keep the slides on their narration. Video is counted in
// frames, so a held slide is `-frames:v N` and never a duration ffmpeg has
// to round. Audio is counted in samples, so each narration is padded with
// silence and cut at frames x samples-per-frame: the track lands on every
// scene boundary exactly.

// sampleRate divides by every frame rate the harness accepts, so one frame
// is a whole number of samples.
const sampleRate = 48_000

// Matched to what Remotion writes for the entrances. A held slide encoded in
// another pixel format, colour range or time scale concatenates without an
// error and plays at the wrong speed, so these are not cosmetic.
const (
	stillPreset    = "veryfast"
	stillCRF       = "18"
	pixelFormat    = "yuvj420p"
	colorRange     = "pc"
	timeScale      = "90000"
	narrationRate  = "128k"
	stageDirectory = "scene-parts"
)

func samplesPerFrame(fps int) (int, error) {
	if fps <= 0 || sampleRate%fps != 0 {
		return 0, fmt.Errorf("%d fps does not divide the %d Hz sample rate", fps, sampleRate)
	}
	return sampleRate / fps, nil
}

// scenePart is what Remotion rendered for one scene, and its narration.
type scenePart struct {
	Number    int
	Entrance  string
	Still     string
	Narration string
}

// assemblyPlan is everything ffmpeg does, in order.
type assemblyPlan struct {
	ManifestPath string
	Manifest     string
	Commands     [][]string
}

func planAssembly(parts []scenePart, grid timeline, workdir, output string) (assemblyPlan, error) {
	if len(parts) != len(grid.Scenes) {
		return assemblyPlan{}, errors.New("the assembly needs one part per scene")
	}
	perFrame, err := samplesPerFrame(grid.FPS)
	if err != nil {
		return assemblyPlan{}, err
	}
	stage := path.Join(workdir, stageDirectory)
	plan := assemblyPlan{ManifestPath: path.Join(stage, "video-parts.txt")}
	var manifest, inputs, chains, labels []string
	for i, part := range parts {
		scene := grid.Scenes[i]
		if part.Number != scene.Number {
			return assemblyPlan{}, errors.New("the scene parts are not in the timeline's order")
		}
		manifest = append(manifest, "file '"+part.Entrance+"'")
		if scene.stillFrames() > 0 {
			held := path.Join(stage, "hold-"+strconv.Itoa(scene.Number)+".mp4")
			plan.Commands = append(plan.Commands, []string{
				"ffmpeg", "-nostdin", "-y", "-v", "error", "-loop", "1", "-framerate", strconv.Itoa(grid.FPS),
				"-i", part.Still, "-frames:v", strconv.Itoa(scene.stillFrames()),
				"-c:v", "libx264", "-preset", stillPreset, "-tune", "stillimage", "-crf", stillCRF,
				"-pix_fmt", pixelFormat, "-color_range", colorRange, "-video_track_timescale", timeScale,
				"-an", held,
			})
			manifest = append(manifest, "file '"+held+"'")
		}
		inputs = append(inputs, "-i", part.Narration)
		chains = append(chains, fmt.Sprintf("[%d:a]aformat=sample_fmts=fltp:sample_rates=%d:channel_layouts=mono,apad,atrim=end_sample=%d,asetpts=N/SR/TB[a%d]",
			i, sampleRate, scene.Frames*perFrame, i))
		labels = append(labels, fmt.Sprintf("[a%d]", i))
	}
	plan.Manifest = strings.Join(manifest, "\n") + "\n"
	video := path.Join(stage, "video.mp4")
	narration := path.Join(stage, "narration.m4a")
	plan.Commands = append(plan.Commands,
		[]string{"ffmpeg", "-nostdin", "-y", "-v", "error", "-f", "concat", "-safe", "0", "-i", plan.ManifestPath, "-c", "copy", "-an", video},
		append(append([]string{"ffmpeg", "-nostdin", "-y", "-v", "error"}, inputs...),
			"-filter_complex", strings.Join(chains, ";")+";"+strings.Join(labels, "")+fmt.Sprintf("concat=n=%d:v=0:a=1[narration]", len(parts)),
			"-map", "[narration]", "-c:a", "aac", "-b:a", narrationRate, "-ar", strconv.Itoa(sampleRate), "-ac", "1", narration),
		[]string{"ffmpeg", "-nostdin", "-y", "-v", "error", "-i", video, "-i", narration, "-c:v", "copy", "-c:a", "copy", "-movflags", "+faststart", output},
	)
	return plan, nil
}

// assembleFromStills renders the scene parts and joins them into output,
// and returns the grid it was planned on.
func (h harness) assembleFromStills(ctx context.Context, props string, voices []voiced, output string) (timeline, error) {
	grid, err := buildTimeline(voices, fps, minFrames, entranceFrames(fps))
	if err != nil {
		return timeline{}, err
	}
	request := make([]int, len(grid.Scenes))
	for i, scene := range grid.Scenes {
		request[i] = scene.Entrance
	}
	encoded, _ := json.Marshal(request)
	requestPath := path.Join(h.workdir, "scene-parts-entrance.json")
	if err := h.box.WriteFile(ctx, requestPath, encoded); err != nil {
		return timeline{}, fmt.Errorf("could not write the entrance request: %w", err)
	}
	stage := path.Join(h.workdir, stageDirectory)
	err = h.gated(ctx, func() error {
		if err := h.checked(ctx, h.node("--scene-parts", props, stage, requestPath), h.long, "render the video's scene parts"); err != nil {
			return err
		}
		rendered, err := h.box.ReadFile(ctx, path.Join(stage, "parts.json"), 1<<20)
		if err != nil {
			return fmt.Errorf("could not read the scene parts' manifest: %w", err)
		}
		var manifest struct {
			Parts []struct {
				Number   int    `json:"slide_number"`
				Entrance string `json:"entrance_video"`
				Still    string `json:"still_image"`
				Frames   int    `json:"entrance_frames"`
			} `json:"parts"`
		}
		if err := json.Unmarshal(rendered, &manifest); err != nil {
			return fmt.Errorf("Remotion wrote an unreadable scene-part manifest: %w", err)
		}
		frames := map[int]int{}
		byNumber := map[int]scenePart{}
		for _, part := range manifest.Parts {
			frames[part.Number] = part.Frames
			byNumber[part.Number] = scenePart{Number: part.Number, Entrance: part.Entrance, Still: part.Still,
				Narration: path.Join(h.workdir, "public", audioName(part.Number))}
		}
		if grid, err = grid.withRendered(frames); err != nil {
			return err
		}
		parts := make([]scenePart, len(grid.Scenes))
		for i, scene := range grid.Scenes {
			parts[i] = byNumber[scene.Number]
		}
		plan, err := planAssembly(parts, grid, h.workdir, output)
		if err != nil {
			return err
		}
		if err := h.box.WriteFile(ctx, plan.ManifestPath, []byte(plan.Manifest)); err != nil {
			return fmt.Errorf("could not write the assembly's manifest: %w", err)
		}
		commands := make([]string, len(plan.Commands))
		for i, command := range plan.Commands {
			words := make([]string, len(command))
			for j, word := range command {
				words[j] = quote(word)
			}
			commands[i] = strings.Join(words, " ")
		}
		return h.checked(ctx, strings.Join(commands, " && "), h.long, "assemble the video from its scene parts")
	})
	if err != nil {
		return timeline{}, err
	}
	return grid, h.onGrid(ctx, output, grid)
}

// onGrid refuses an assembled file that is not on the grid it was planned
// on. Concatenating without re-encoding is silent about mismatched streams:
// the command succeeds and the file plays at the wrong speed.
func (h harness) onGrid(ctx context.Context, output string, grid timeline) error {
	result, err := h.box.RunWithin(ctx, "ffprobe -v error -show_entries stream=codec_type,nb_frames,duration -of json -- "+quote(output), h.short)
	if err != nil || !result.OK() {
		return fmt.Errorf("could not probe the assembled video: %v %s", err, tail(result.Output, 2000))
	}
	var probed struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Frames   string `json:"nb_frames"`
			Duration string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal([]byte(result.Output), &probed); err != nil {
		return fmt.Errorf("could not read the assembled video's streams: %w", err)
	}
	tolerance := big.NewRat(1, int64(grid.FPS))
	for _, kind := range []string{"video", "audio"} {
		found := false
		for _, stream := range probed.Streams {
			if stream.Type != kind {
				continue
			}
			found = true
			if kind == "video" {
				if frames, err := strconv.Atoi(stream.Frames); err != nil || frames != grid.totalFrames() {
					return fmt.Errorf("the assembled video holds %s frames, not the planned %d", stream.Frames, grid.totalFrames())
				}
			}
			measured, ok := new(big.Rat).SetString(stream.Duration)
			if !ok {
				return fmt.Errorf("the assembled %s reports no duration", kind)
			}
			if new(big.Rat).Abs(new(big.Rat).Sub(measured, grid.seconds())).Cmp(tolerance) > 0 {
				planned, _ := grid.seconds().Float64()
				return fmt.Errorf("the assembled %s runs %ss, not the planned %.3fs", kind, stream.Duration, planned)
			}
			break
		}
		if !found {
			return fmt.Errorf("the assembled video has no %s stream", kind)
		}
	}
	return nil
}
