import { useEffect, useLayoutEffect, useRef } from "react";

const tailThresholdPixels = 48;

const scrollParent = (element: HTMLElement) => {
  let candidate = element.parentElement;
  while (candidate) {
    const { overflowY } = window.getComputedStyle(candidate);
    if (overflowY === "auto" || overflowY === "scroll") {
      return candidate;
    }
    candidate = candidate.parentElement;
  }
};

export const useLogTail = <Element extends HTMLElement>(
  contentVersion: string
) => {
  const contentRef = useRef<Element>(null);
  const followsTailRef = useRef(true);

  useEffect(() => {
    const content = contentRef.current;
    if (!content) {
      return;
    }
    const viewport = scrollParent(content);
    if (!viewport) {
      return;
    }
    const updateFollowState = () => {
      const distanceFromTail =
        viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop;
      followsTailRef.current = distanceFromTail <= tailThresholdPixels;
    };
    viewport.addEventListener("scroll", updateFollowState, { passive: true });
    return () => viewport.removeEventListener("scroll", updateFollowState);
  }, [contentVersion]);

  useLayoutEffect(() => {
    const content = contentRef.current;
    if (!content || !followsTailRef.current) {
      return;
    }
    const viewport = scrollParent(content);
    if (viewport) {
      viewport.scrollTop = viewport.scrollHeight;
      return;
    }
    content.scrollIntoView({ block: "end" });
  }, [contentVersion]);

  return contentRef;
};
