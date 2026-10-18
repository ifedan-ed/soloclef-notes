import {interpolate, spring} from "remotion";

export function createStagger(totalFrames: number) {
  return function stagger(
    frame: number,
    fps: number,
    index: number,
    total: number,
  ): {opacity: number; transform: string} {
    const enterPhase = Math.floor(totalFrames * 0.2);
    const exitStart = Math.floor(totalFrames * 0.8);
    const gap = Math.max(6, Math.floor(enterPhase / Math.max(total, 1)));
    const delay = index * gap;
    const entrance = spring({
      frame: Math.max(0, frame - delay),
      fps,
      config: {damping: 15, stiffness: 120, mass: 0.8},
    });
    const exit = interpolate(frame, [exitStart, totalFrames], [0, 1], {
      extrapolateLeft: "clamp",
      extrapolateRight: "clamp",
    });
    const ambient = entrance > 0.99 ? Math.sin(frame * 0.05) * 2 : 0;
    const translateY =
      interpolate(entrance, [0, 1], [40, 0]) +
      interpolate(exit, [0, 1], [0, -30]) +
      ambient;

    return {
      opacity: entrance * (1 - exit),
      transform: `translateY(${translateY}px) scale(${interpolate(entrance, [0, 1], [0.97, 1])})`,
    };
  };
}

// What scenes import. A scene is as long as its narration, which the author
// does not know, so this helper only brings an element in and then leaves it on
// screen: the old default was createStagger(300), which faded every staggered
// element out between seconds 8 and 10 and left the rest of a 20-40 s scene
// showing only its unstaggered heading. Scenes cut to the next with nothing
// faded: the still review samples each scene's last frame, and a fade there
// reads as a blank slide.
export function stagger(
  frame: number,
  fps: number,
  index: number,
  total: number,
): {opacity: number; transform: string} {
  const gap = Math.max(6, Math.floor((fps * 2) / Math.max(total, 1)));
  const entrance = spring({
    frame: Math.max(0, frame - index * gap),
    fps,
    config: {damping: 15, stiffness: 120, mass: 0.8},
  });
  return {
    opacity: entrance,
    transform: `translateY(${interpolate(entrance, [0, 1], [40, 0])}px) scale(${interpolate(entrance, [0, 1], [0.97, 1])})`,
  };
}
