// Generated from api/openapi.json by web/scripts/generate-api.mjs. Do not edit.

export type AnalysisOptions = {
  "allow_visual": boolean;
  "aspect": string;
  "confirmed": boolean;
  "duration": number;
  "goals": Array<string>;
  "instruction": string;
  "language": string;
  "mode": string;
}

export type Candidate = {
  "end": number;
  "evidence": string;
  "id": string;
  "kind": string;
  "label": string;
  "score": number;
  "start": number;
}

export type Cue = {
  "end": number;
  "start": number;
  "text": string;
}

export type Draft = {
  "aspect": string;
  "crop_x": number;
  "hook": string;
  "id": string;
  "language": string;
  "layout": string;
  "origin": string;
  "original_audio": boolean;
  "parent_draft_id"?: (string | null);
  "parent_revision"?: (number | null);
  "project_id"?: string;
  "revision": number;
  "scenes": Array<Scene>;
  "subtitles": boolean;
  "title": string;
  "title_accent": (string | null);
  "title_motion": boolean;
  "title_scale": number;
  "title_style": string;
  "title_template_version": number;
  "title_y": number;
  "updated_at": string;
}

export type Error = {
  "code": string;
  "message": string;
  "request_id": string;
  "retryable": boolean;
}

export type Export = {
  "created_at": string;
  "draft_id": string;
  "revision": number;
  "task_id": string;
  "title": string;
}

export type ModelSettings = {
  "api_key": string;
  "base_url": string;
  "model": string;
}

export type ModelStatus = {
  "base_url": string;
  "configured": boolean;
  "model": string;
}

export type Project = {
  "created_at": string;
  "duration": number;
  "error"?: string;
  "height": number;
  "id": string;
  "name": string;
  "status": string;
  "updated_at": string;
  "url"?: string;
  "width": number;
}

export type Scene = {
  "end": number;
  "evidence": string;
  "id": string;
  "label": string;
  "start": number;
}

export type Task = {
  "cancel_requested": boolean;
  "completed_steps": Array<string>;
  "created_at": string;
  "error"?: string;
  "heartbeat": string;
  "id": string;
  "kind": string;
  "payload"?: unknown;
  "progress": (number | null);
  "project_id": string;
  "retryable": boolean;
  "stage": string;
  "status": string;
  "updated_at": string;
}

export type Workspace = {
  "candidates": Array<Candidate>;
  "drafts": Array<Draft>;
  "exports": Array<Export>;
  "project": Project;
  "tasks": Array<Task>;
}
