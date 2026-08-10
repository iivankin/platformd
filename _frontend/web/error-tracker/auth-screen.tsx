import { KeyRound, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

import { api, setAdminToken } from "./api";
import { errorMessage } from "./format";

export const AuthScreen = ({
  onSuccess,
}: {
  onSuccess: () => Promise<void>;
}) => {
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const value = token.trim();
    if (!value || pending) {
      return;
    }
    setPending(true);
    setError("");
    setAdminToken(value);
    try {
      await api.tracker();
      await onSuccess();
    } catch (authError) {
      setAdminToken("");
      setError(errorMessage(authError, "Authentication failed"));
      setToken("");
      inputRef.current?.focus();
    } finally {
      setPending(false);
    }
  };

  return (
    <main className="relative grid h-full place-items-center overflow-hidden bg-background">
      <div className="tracker-grid pointer-events-none absolute inset-0 opacity-40" />
      <form
        className="relative z-10 flex w-72 animate-in flex-col items-center gap-8 duration-300 fade-in slide-in-from-bottom-2"
        onSubmit={submit}
      >
        <div className="grid size-14 place-items-center border border-border bg-card text-sm font-bold">
          et
        </div>
        <div className="w-full space-y-1.5 text-center">
          <h1 className="text-[11px] font-semibold tracking-[0.2em] uppercase">
            Access required
          </h1>
          <p className="text-[10px] leading-relaxed text-muted-foreground">
            Enter the tracker admin token
          </p>
        </div>
        <div className="w-full space-y-3">
          <div className="relative">
            <KeyRound className="pointer-events-none absolute top-1/2 left-3 size-3 -translate-y-1/2 text-muted-foreground" />
            <Input
              autoComplete="off"
              className="h-9 bg-card pr-3 pl-8"
              minLength={24}
              onChange={(event) => {
                setToken(event.target.value);
                if (error) {
                  setError("");
                }
              }}
              placeholder="Admin token"
              ref={inputRef}
              spellCheck={false}
              type="password"
              value={token}
            />
          </div>
          <Button
            className="h-9 w-full"
            disabled={!token.trim() || pending}
            type="submit"
          >
            {pending ? <LoaderCircle className="animate-spin" /> : null}
            {pending ? "Verifying…" : "Continue"}
          </Button>
        </div>
        <div className="h-4 text-center">
          {error ? (
            <p className="text-[10px] text-destructive">{error}</p>
          ) : null}
        </div>
      </form>
    </main>
  );
};
