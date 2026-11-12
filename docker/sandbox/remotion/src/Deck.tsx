import {Audio} from "@remotion/media";
import type React from "react";
import {type ComponentType, useEffect, useRef, useState} from "react";
import {
  AbsoluteFill,
  continueRender,
  delayRender,
  interpolate,
  Series,
  staticFile,
  useCurrentFrame,
  useVideoConfig,
} from "remotion";
import {LAYOUT_MARK, type LayoutReport, measureScene} from "./layoutCheck";
import {sceneComponents} from "./scenes/generated";

export type SceneInput = {
  slide_number: number;
  code: string;
  audio?: string;
};

export type DeckProps = {
  fps: number;
  min_duration_in_frames: number;
  scenes: SceneInput[];
  sceneDurations?: number[];
  // layout_check is set by render.mjs for --stills only: each scene's last
  // frame is then measured (layoutCheck.ts) before it is captured.
  layout_check?: boolean;
};

const Watermark: React.FC = () => {
  const frame = useCurrentFrame();
  const {fps} = useVideoConfig();
  const opacity = interpolate(frame, [0, fps * 0.5], [0, 0.68], {
    extrapolateRight: "clamp",
  });

  return (
    <div
      style={{
        position: "absolute",
        bottom: 28,
        right: 36,
        display: "grid",
        placeItems: "center",
        width: 44,
        height: 44,
        alignItems: "center",
        borderRadius: 9999,
        background: "rgba(0, 0, 0, 0.2)",
        backdropFilter: "blur(12px)",
        border: "1px solid rgba(255, 255, 255, 0.12)",
        pointerEvents: "none",
        zIndex: 9999,
        opacity,
      }}
    >
      <img
        src={staticFile("icon-128.svg")}
        alt=""
        style={{
          width: 28,
          height: 28,
          objectFit: "contain",
          filter: "brightness(0) invert(1)",
          opacity: 0.82,
        }}
      />
    </div>
  );
};

// Measure checks the scene drawn in root once Chrome has laid it out with
// its fonts, and prints the findings on the line render.mjs reads. The
// frame is held (delayRender) until then, so the still that is captured is
// the layout that was measured.
const Measure: React.FC<{
  root: React.RefObject<HTMLDivElement | null>;
  slideNumber: number;
}> = ({root, slideNumber}) => {
  // Called while rendering, before Remotion can take the screenshot; an
  // effect would run too late to hold it.
  const [handle] = useState(() =>
    delayRender(`Measuring the layout of slide ${slideNumber}`),
  );
  useEffect(() => {
    let live = true;
    document.fonts.ready
      .then(
        () =>
          // One frame after the fonts: the layout they change is settled.
          new Promise<void>((resolve) => requestAnimationFrame(() => resolve())),
      )
      .then(() => {
        if (!live) return;
        const report: LayoutReport = {
          slide_number: slideNumber,
          findings: root.current
            ? measureScene(root.current)
            : ["the scene was not drawn"],
        };
        console.log(LAYOUT_MARK + JSON.stringify(report));
        continueRender(handle);
      });
    return () => {
      live = false;
    };
  }, [handle, root, slideNumber]);
  return null;
};

// SceneFrame draws a scene in a frame of its own, so the layout check
// measures that scene and not the watermark or anything Remotion adds. It
// is the same full-frame box the sequence gives, drawn in every mode, so
// the layout measured in the stills is the layout of the video.
const SceneFrame: React.FC<{
  Scene: ComponentType;
  slideNumber: number;
  durationInFrames: number;
  check: boolean;
}> = ({Scene, slideNumber, durationInFrames, check}) => {
  const frame = useCurrentFrame();
  const root = useRef<HTMLDivElement>(null);
  return (
    <AbsoluteFill ref={root}>
      <Scene />
      {check && frame === durationInFrames - 1 ? (
        <Measure root={root} slideNumber={slideNumber} />
      ) : null}
    </AbsoluteFill>
  );
};

export const Deck: React.FC<DeckProps> = ({
  scenes,
  sceneDurations = [],
  layout_check = false,
}) => {
  const {fps} = useVideoConfig();
  if (scenes.length !== sceneComponents.length) {
    throw new Error(
      `Compiled ${sceneComponents.length} scenes, received ${scenes.length} scene props`,
    );
  }

  return (
    <AbsoluteFill>
      <Series>
        {scenes.map((scene, index) => {
          const Scene = sceneComponents[index] as ComponentType;
          const durationInFrames = sceneDurations[index];
          if (!durationInFrames) {
            throw new Error(`Missing duration for slide ${scene.slide_number}`);
          }
          return (
            <Series.Sequence
              key={scene.slide_number}
              durationInFrames={durationInFrames}
              premountFor={fps}
            >
              <SceneFrame
                Scene={Scene}
                slideNumber={scene.slide_number}
                durationInFrames={durationInFrames}
                check={layout_check}
              />
              {scene.audio ? <Audio src={staticFile(scene.audio)} /> : null}
            </Series.Sequence>
          );
        })}
      </Series>
      <Watermark />
    </AbsoluteFill>
  );
};
