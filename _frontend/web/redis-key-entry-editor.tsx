import { Save } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import type { RedisMutationInput, RedisPreview } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RedisBinaryInput } from "@/redis-binary-input";
import { redisInputBytes } from "@/redis-data-utils";
import type { RedisInputEncoding } from "@/redis-data-utils";

interface RedisKeyEntryEditorProperties {
  apply: (input: RedisMutationInput) => Promise<void>;
  busy: boolean;
  keyBase64: string;
  preview: RedisPreview;
}

interface EntryValues {
  field: string;
  fieldEncoding: RedisInputEncoding;
  member: string;
  memberEncoding: RedisInputEncoding;
  score: number;
  value: string;
  valueEncoding: RedisInputEncoding;
}

const entryMutation = (
  type: string,
  key: string,
  values: EntryValues
): RedisMutationInput => {
  const value = redisInputBytes(values.value, values.valueEncoding);
  switch (type) {
    case "string": {
      return { key, operation: "string_set", value };
    }
    case "hash": {
      return {
        field: redisInputBytes(values.field, values.fieldEncoding),
        key,
        operation: "hash_set",
        value,
      };
    }
    case "list": {
      return { key, operation: "list_push_right", value };
    }
    case "set": {
      return {
        key,
        member: redisInputBytes(values.member, values.memberEncoding),
        operation: "set_add",
      };
    }
    case "zset": {
      return {
        key,
        member: redisInputBytes(values.member, values.memberEncoding),
        operation: "zset_add",
        score: values.score,
      };
    }
    case "stream": {
      return {
        fields: [
          {
            field: redisInputBytes(values.field, values.fieldEncoding),
            value,
          },
        ],
        key,
        operation: "stream_add",
      };
    }
    default: {
      throw new Error(`Unsupported Redis type ${type}`);
    }
  }
};

export const RedisKeyEntryEditor = ({
  apply,
  busy,
  keyBase64,
  preview,
}: RedisKeyEntryEditorProperties) => {
  const [firstPreviewItem] = preview.items;
  const [initialStringValue] = firstPreviewItem?.values ?? [];
  const [field, setField] = useState("");
  const [value, setValue] = useState(
    preview.type === "string"
      ? (initialStringValue?.text ?? initialStringValue?.base64 ?? "")
      : ""
  );
  const [member, setMember] = useState("");
  const [score, setScore] = useState("0");
  const [index, setIndex] = useState("0");
  const [fieldEncoding, setFieldEncoding] =
    useState<RedisInputEncoding>("text");
  const [valueEncoding, setValueEncoding] = useState<RedisInputEncoding>(
    preview.type === "string" && initialStringValue?.text === undefined
      ? "base64url"
      : "text"
  );
  const [memberEncoding, setMemberEncoding] =
    useState<RedisInputEncoding>("text");
  const usesField = preview.type === "hash" || preview.type === "stream";
  const usesMember = preview.type === "set" || preview.type === "zset";
  const usesValue = !usesMember;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void apply(
      entryMutation(preview.type, keyBase64, {
        field,
        fieldEncoding,
        member,
        memberEncoding,
        score: Number(score),
        value,
        valueEncoding,
      })
    );
  };

  return (
    <>
      <form className="border-b border-border px-4 py-4" onSubmit={submit}>
        {usesField ? (
          <RedisBinaryInput
            encoding={fieldEncoding}
            label="Field"
            onEncodingChange={setFieldEncoding}
            onValueChange={setField}
            value={field}
          />
        ) : null}
        {usesMember ? (
          <RedisBinaryInput
            encoding={memberEncoding}
            label="Member"
            onEncodingChange={setMemberEncoding}
            onValueChange={setMember}
            value={member}
          />
        ) : null}
        {usesValue ? (
          <RedisBinaryInput
            encoding={valueEncoding}
            label={preview.type === "string" ? "Value" : "New value"}
            multiline={preview.type === "string"}
            onEncodingChange={setValueEncoding}
            onValueChange={setValue}
            value={value}
          />
        ) : null}
        {preview.type === "zset" ? (
          <label
            className="mb-3 block text-[9px] tracking-[0.1em] text-muted-foreground uppercase"
            htmlFor="redis-editor-score"
          >
            Score
            <Input
              className="mt-1.5"
              id="redis-editor-score"
              onChange={(event) => setScore(event.target.value)}
              type="number"
              value={score}
            />
          </label>
        ) : null}
        <div className="flex justify-end">
          <Button disabled={busy} size="sm" type="submit">
            <Save />
            {preview.type === "string" ? "Save value" : "Add item"}
          </Button>
        </div>
      </form>

      {preview.type === "list" ? (
        <form
          className="grid grid-cols-[5rem_1fr_auto] gap-2 border-b border-border px-4 py-3"
          onSubmit={(event) => {
            event.preventDefault();
            void apply({
              index: Number(index),
              key: keyBase64,
              operation: "list_set",
              value: redisInputBytes(value, valueEncoding),
            });
          }}
        >
          <Input
            aria-label="List index"
            onChange={(event) => setIndex(event.target.value)}
            type="number"
            value={index}
          />
          <Input
            aria-label="List replacement value"
            onChange={(event) => setValue(event.target.value)}
            placeholder="Replacement value"
            value={value}
          />
          <Button disabled={busy} size="sm" type="submit" variant="outline">
            Set index
          </Button>
        </form>
      ) : null}
    </>
  );
};
