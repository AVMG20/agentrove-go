export interface Artifact {
  id: string;
  chat_id: string | null;
  title: string;
  is_public: boolean;
  version: number;
  // Pinned artifacts lead the list, most recently pinned first.
  pinned_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface ArtifactView extends Artifact {
  is_owner: boolean;
  // Relative to the API base; carries a short-lived view token when private.
  content_path: string;
}

export interface ArtifactUpdateRequest {
  title?: string;
  is_public?: boolean;
  pinned?: boolean;
}
