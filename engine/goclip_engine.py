#!/usr/bin/env python3
"""Line-delimited JSON bridge from the Go worker to AutoClip's media engine.

This program deliberately owns no HTTP server, queue, database or persistent
settings.  It receives one job on stdin, writes private artifacts below its
provided workspace, and reports only structured progress/results on stdout.
"""

from __future__ import annotations

import asyncio
import json
import sys
from pathlib import Path
from typing import Any


def emit(kind: str, **payload: Any) -> None:
    print(json.dumps({"type": kind, **payload}, ensure_ascii=False), flush=True)


def require(value: Any, name: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{name} is required")
    return value


def progress(stage: str, fraction: float) -> None:
    emit("progress", stage=stage, percent=max(0.0, min(100.0, fraction * 100.0)))


async def analyze(job: dict[str, Any]) -> dict[str, Any]:
    # Imports stay inside the job so a missing engine dependency becomes a
    # structured task failure instead of contaminating the Go process.
    from autoclip.config import ProviderSettings, WhisperSettings
    from autoclip.pipeline import highlights, prepare, transcribe
    from autoclip.pipeline.reframe import ReframeConfig, build_crop_path
    from autoclip.pipeline.transcript import Transcript
    from autoclip.providers import base as provider_base
    from autoclip.providers.base import DetectionConfig
    from autoclip.providers.openai_provider import OpenAIProvider

    source = Path(require(job.get("source"), "source")).resolve()
    workspace = Path(require(job.get("workspace"), "workspace")).resolve()
    if not source.is_file():
        raise ValueError("source is not a readable media file")
    workspace.mkdir(mode=0o700, parents=True, exist_ok=True)
    # Keep GoClip's Chinese, evidence-first highlight rubric while reusing the
    # upstream provider validation and word-indexed boundary logic.
    provider_base.PROMPT_DIR = Path(__file__).resolve().parent / "prompts"
    crops = workspace / "crops"
    crops.mkdir(mode=0o700, exist_ok=True)

    settings = job.get("settings")
    if not isinstance(settings, dict):
        raise ValueError("settings must be an object")
    model = require(settings.get("model"), "settings.model")
    base_url = require(settings.get("base_url"), "settings.base_url")
    api_key = require(settings.get("api_key"), "settings.api_key")
    duration = float(job.get("duration", 0))
    if duration <= 0:
        raise ValueError("duration must be positive")

    audio = workspace / "audio.wav"
    if not audio.exists():
        progress("prepare", 0)
        prepare.extract_audio(source, audio, duration_s=duration, on_progress=lambda p: progress("prepare", p))
    progress("prepare", 1)

    transcript_path = workspace / "transcript.json"
    if transcript_path.exists():
        transcript = Transcript.load(transcript_path)
    else:
        whisper = WhisperSettings(
            model=str(settings.get("whisper_model") or "small"),
            language=str(settings.get("language") or ""),
            diarization=False,
        )
        transcript = transcribe.transcribe(
            audio, whisper, duration_s=duration, on_progress=lambda p: progress("transcribe", p)
        )
        transcript.save(transcript_path)
    progress("transcribe", 1)

    silence_path = workspace / "silences.json"
    if silence_path.exists():
        raw = json.loads(silence_path.read_text(encoding="utf-8"))
        silences = [prepare.Silence(**item) for item in raw]
    else:
        silences = prepare.detect_silences(audio)
        silence_path.write_text(json.dumps([{"start": s.start, "end": s.end} for s in silences]), encoding="utf-8")

    detector = DetectionConfig(
        min_duration_s=float(settings.get("min_duration_s", 20)),
        max_duration_s=float(settings.get("max_duration_s", 90)),
        max_clips=int(settings.get("max_clips", 10)),
        language=str(settings.get("language") or ""),
        prompt_version="highlight_v1",
    )
    provider = OpenAIProvider(model, api_key=api_key, base_url=base_url)
    clips = await highlights.detect(
        transcript, provider, detector, job_id=str(job.get("job_id") or "goclip"), silences=silences,
        on_progress=lambda p: progress("highlights", p),
    )
    progress("highlights", 1)

    ratio = str(settings.get("ratio") or "9:16")
    aspects = {"9:16": (9, 16), "1:1": (1, 1), "16:9": (16, 9)}
    if ratio not in aspects:
        raise ValueError("ratio must be 9:16, 1:1 or 16:9")
    aspect_w, aspect_h = aspects[ratio]
    output: list[dict[str, Any]] = []
    for index, clip in enumerate(clips):
        crop = build_crop_path(
            source, start_s=clip.start_s, end_s=clip.end_s, transcript=transcript,
            config=ReframeConfig(aspect_w=aspect_w, aspect_h=aspect_h),
        )
        crop_path = crops / f"{clip.id}.json"
        crop.save(crop_path)
        output.append({
            "id": clip.id, "start": clip.start_s, "end": clip.end_s,
            "start_word": clip.start_word, "end_word": clip.end_word,
            "title": clip.title, "hook": clip.hook, "score": clip.score,
            "reason": clip.reason, "crop_path": str(crop_path),
        })
        progress("reframe", (index + 1) / len(clips))
    progress("reframe", 1)
    return {
        "candidates": output,
        "words": [{"text": w.text, "start": w.start, "end": w.end, "speaker": w.speaker} for w in transcript.words],
    }


def export(job: dict[str, Any]) -> dict[str, Any]:
    """Render one reviewed candidate using AutoClip's crop and caption pipeline."""
    from autoclip.config import ExportSettings
    from autoclip.pipeline.captions import get_style
    from autoclip.pipeline.export import ExportRequest, export_clip
    from autoclip.pipeline.reframe.croppath import CropPath
    from autoclip.pipeline.transcript import Word

    source = Path(require(job.get("source"), "source")).resolve()
    workspace = Path(require(job.get("workspace"), "workspace")).resolve()
    payload = job.get("export")
    if not source.is_file() or not isinstance(payload, dict):
        raise ValueError("source and export payload are required")
    destination = Path(require(payload.get("destination"), "export.destination")).resolve()
    crop_path = Path(require(payload.get("crop_path"), "export.crop_path")).resolve()
    if workspace not in destination.parents or workspace not in crop_path.parents:
        raise ValueError("engine export paths must stay inside its workspace")
    start, end = float(job.get("start", 0)), float(job.get("end", 0))
    if start < 0 or end <= start:
        raise ValueError("invalid export boundaries")
    words_raw = payload.get("words")
    if not isinstance(words_raw, list):
        raise ValueError("export.words must be an array")
    words = [Word(text=require(w.get("text"), "word.text"), start=float(w["start"]), end=float(w["end"]), speaker=w.get("speaker")) for w in words_raw if isinstance(w, dict)]
    if len(words) != len(words_raw):
        raise ValueError("invalid export words")
    ratio = str(payload.get("ratio") or "9:16")
    style = get_style(str(payload.get("caption_style") or "bold_pop"))
    progress("export", 0)
    output = export_clip(
        ExportRequest(source=source, destination=destination, start_s=start, end_s=end,
                      crop_path=CropPath.load(crop_path), words=words, style=style, ratio=ratio),
        work_dir=workspace, settings=ExportSettings(ratio=ratio, caption_style=style.key),
        on_progress=lambda p: progress("export", p),
    )
    progress("export", 1)
    return {"output": str(output)}


def reframe(job: dict[str, Any]) -> dict[str, Any]:
    """Build a local face-tracked crop path for an already selected clip."""
    from autoclip.pipeline.reframe import ReframeConfig, build_crop_path

    source = Path(require(job.get("source"), "source")).resolve()
    workspace = Path(require(job.get("workspace"), "workspace")).resolve()
    start, end = float(job.get("start", 0)), float(job.get("end", 0))
    if not source.is_file() or end <= start or start < 0:
        raise ValueError("source and valid reframe boundaries are required")
    settings = job.get("settings")
    if not isinstance(settings, dict):
        raise ValueError("settings must be an object")
    ratio = str(settings.get("ratio") or "9:16")
    aspects = {"9:16": (9, 16), "1:1": (1, 1), "16:9": (16, 9)}
    if ratio not in aspects:
        raise ValueError("ratio must be 9:16, 1:1 or 16:9")
    workspace.mkdir(mode=0o700, parents=True, exist_ok=True)
    aspect_w, aspect_h = aspects[ratio]
    progress("reframe", 0)
    crop = build_crop_path(source, start_s=start, end_s=end,
                           config=ReframeConfig(aspect_w=aspect_w, aspect_h=aspect_h))
    path = workspace / "crop.json"
    crop.save(path)
    progress("reframe", 1)
    return {"output": str(path)}


def main() -> int:
    job: dict[str, Any] = {}
    try:
        job = json.loads(sys.stdin.read())
        if not isinstance(job, dict):
            raise ValueError("job must be an object")
        if job.get("operation") == "export":
            result = export(job)
        elif job.get("operation") == "reframe":
            result = reframe(job)
        elif job.get("operation") in (None, "analyze"):
            result = asyncio.run(analyze(job))
        else:
            raise ValueError("unknown engine operation")
        emit("result", **result)
        return 0
    except Exception as exc:  # The Go parent deliberately exposes no traceback or secrets.
        message = str(exc)
        settings = job.get("settings") if isinstance(job, dict) else None
        if isinstance(settings, dict) and isinstance(settings.get("api_key"), str):
            message = message.replace(settings["api_key"], "[redacted]")
        emit("error", message=message[:1000])
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
