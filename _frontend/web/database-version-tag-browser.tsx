import { ChevronLeft, ChevronRight, Search } from "lucide-react";

import type { ManagedImageEngine } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { DatabaseVersionChangeState } from "@/use-database-version-change";

interface DatabaseVersionTagBrowserProperties {
  change: DatabaseVersionChangeState;
  engine: ManagedImageEngine;
}

export const DatabaseVersionTagBrowser = ({
  change,
  engine,
}: DatabaseVersionTagBrowserProperties) => (
  <>
    <form
      className="flex gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        void change.loadTags(1, change.tagSearch);
      }}
    >
      <Input
        aria-label="Search official image tags"
        onChange={(event) => change.setTagSearch(event.target.value)}
        placeholder="Search official tags"
        value={change.tagSearch}
      />
      <Button
        disabled={change.tagsLoading}
        size="sm"
        type="submit"
        variant="outline"
      >
        <Search />
        Search
      </Button>
    </form>

    {change.tagPage ? (
      <div className="mt-3 border-y border-border py-2">
        <div className="flex max-h-24 flex-wrap gap-1 overflow-y-auto">
          {change.tagPage.tags.map((tag) => (
            <Button
              key={tag.name}
              onClick={() => change.selectTargetTag(tag.name)}
              size="sm"
              type="button"
              variant={change.targetTag === tag.name ? "secondary" : "ghost"}
            >
              {tag.name}
            </Button>
          ))}
          {change.tagPage.tags.length === 0 ? (
            <p className="px-1 py-2 text-[9px] text-muted-foreground">
              No tags on this page. Manual input remains available.
            </p>
          ) : null}
        </div>
        <div className="mt-2 flex items-center justify-between text-[9px] text-muted-foreground">
          <span>
            Page {change.tagPage.page} · {change.tagPage.total.toLocaleString()}{" "}
            tags
          </span>
          <div className="flex gap-1">
            <Button
              aria-label="Previous image tag page"
              disabled={!change.tagPage.previousPage || change.tagsLoading}
              onClick={() =>
                void change.loadTags(
                  change.tagPage?.previousPage ?? 1,
                  change.tagSearch
                )
              }
              size="icon"
              type="button"
              variant="ghost"
            >
              <ChevronLeft />
            </Button>
            <Button
              aria-label="Next image tag page"
              disabled={!change.tagPage.nextPage || change.tagsLoading}
              onClick={() =>
                void change.loadTags(
                  change.tagPage?.nextPage ?? 1,
                  change.tagSearch
                )
              }
              size="icon"
              type="button"
              variant="ghost"
            >
              <ChevronRight />
            </Button>
          </div>
        </div>
      </div>
    ) : null}

    {change.tagError ? (
      <p className="mt-2 text-[9px] text-amber-600 dark:text-amber-400">
        {change.tagError}
      </p>
    ) : null}

    <div className="mt-4 flex gap-2">
      <Input
        aria-label="Target official image tag"
        autoCapitalize="none"
        autoComplete="off"
        onChange={(event) => change.selectTargetTag(event.target.value)}
        placeholder={engine === "postgres" ? "18.3" : "8.0"}
        spellCheck={false}
        value={change.targetTag}
      />
      <Button
        disabled={
          !change.targetTag.trim() ||
          change.previewing ||
          change.operation?.status === "running"
        }
        onClick={() => void change.previewTarget()}
        size="sm"
        type="button"
        variant="outline"
      >
        {change.previewing ? "Resolving…" : "Preview"}
      </Button>
    </div>
    <p className="mt-1.5 text-[9px] text-muted-foreground">
      Suggestions come from the official repository. Any manual official tag is
      accepted.
    </p>
  </>
);
