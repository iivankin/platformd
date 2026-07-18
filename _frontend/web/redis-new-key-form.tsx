import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import type { RedisMutationInput } from "@/api";
import { Button } from "@/components/ui/button";
import { FormCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { RedisBinaryInput } from "@/redis-binary-input";
import { redisInputBytes } from "@/redis-data-utils";
import type { RedisInputEncoding } from "@/redis-data-utils";

type NewRedisType = "hash" | "list" | "set" | "stream" | "string" | "zset";

interface RedisNewKeyFormProperties {
  busy: boolean;
  onCancel: () => void;
  onMutate: (input: RedisMutationInput) => Promise<void>;
}

export const RedisNewKeyForm = ({
  busy,
  onCancel,
  onMutate,
}: RedisNewKeyFormProperties) => {
  const [type, setType] = useState<NewRedisType>("string");
  const [key, setKey] = useState("");
  const [keyEncoding, setKeyEncoding] = useState<RedisInputEncoding>("text");
  const [field, setField] = useState("");
  const [fieldEncoding, setFieldEncoding] =
    useState<RedisInputEncoding>("text");
  const [value, setValue] = useState("");
  const [valueEncoding, setValueEncoding] =
    useState<RedisInputEncoding>("text");
  const [score, setScore] = useState("0");
  const [listSide, setListSide] = useState<"left" | "right">("right");
  const [streamFields, setStreamFields] = useState([{ field: "", value: "" }]);
  const [error, setError] = useState<string | null>(null);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    try {
      const encodedKey = redisInputBytes(key, keyEncoding);
      const encodedField = redisInputBytes(field, fieldEncoding);
      const encodedValue = redisInputBytes(value, valueEncoding);
      let input: RedisMutationInput;
      switch (type) {
        case "string": {
          input = {
            key: encodedKey,
            operation: "string_set",
            value: encodedValue,
          };
          break;
        }
        case "hash": {
          input = {
            field: encodedField,
            key: encodedKey,
            operation: "hash_set",
            value: encodedValue,
          };
          break;
        }
        case "list": {
          input = {
            key: encodedKey,
            operation:
              listSide === "left" ? "list_push_left" : "list_push_right",
            value: encodedValue,
          };
          break;
        }
        case "set": {
          input = {
            key: encodedKey,
            member: encodedValue,
            operation: "set_add",
          };
          break;
        }
        case "zset": {
          input = {
            key: encodedKey,
            member: encodedValue,
            operation: "zset_add",
            score: Number(score),
          };
          break;
        }
        case "stream": {
          input = {
            fields: streamFields.map((pair) => ({
              field: redisInputBytes(pair.field, "text"),
              value: redisInputBytes(pair.value, "text"),
            })),
            key: encodedKey,
            operation: "stream_add",
          };
          break;
        }
        default: {
          throw new Error("Unsupported Redis key type");
        }
      }
      await onMutate(input);
    } catch (mutationError) {
      setError(
        mutationError instanceof Error
          ? mutationError.message
          : "Unable to create Redis key"
      );
    }
  };

  return (
    <FormCard className="px-4 py-4" onSubmit={submit}>
      <div className="mb-4 flex items-center justify-between gap-3">
        <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
          New key
        </h3>
        <Select
          items={{
            hash: "Hash",
            list: "List",
            set: "Set",
            stream: "Stream",
            string: "String",
            zset: "Sorted set",
          }}
          onValueChange={(selected) =>
            setType(String(selected) as NewRedisType)
          }
          value={type}
        >
          <SelectTrigger className="h-7 text-[10px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="end">
            <SelectItem value="string">String</SelectItem>
            <SelectItem value="hash">Hash</SelectItem>
            <SelectItem value="list">List</SelectItem>
            <SelectItem value="set">Set</SelectItem>
            <SelectItem value="zset">Sorted set</SelectItem>
            <SelectItem value="stream">Stream</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <RedisBinaryInput
        encoding={keyEncoding}
        label="Key"
        onEncodingChange={setKeyEncoding}
        onValueChange={setKey}
        value={key}
      />
      {type === "hash" ? (
        <RedisBinaryInput
          encoding={fieldEncoding}
          label="Field"
          onEncodingChange={setFieldEncoding}
          onValueChange={setField}
          value={field}
        />
      ) : null}
      {type === "stream" ? (
        <div className="mb-3 border-t border-border">
          {streamFields.map((pair, index) => (
            <div
              className="grid grid-cols-[1fr_1fr_auto] gap-2 border-b border-border py-2"
              key={`stream-field-${index.toString()}`}
            >
              <Input
                aria-label={`Stream field ${index + 1}`}
                onChange={(event) =>
                  setStreamFields((current) =>
                    current.map((item, itemIndex) =>
                      itemIndex === index
                        ? { ...item, field: event.target.value }
                        : item
                    )
                  )
                }
                placeholder="field"
                value={pair.field}
              />
              <Input
                aria-label={`Stream value ${index + 1}`}
                onChange={(event) =>
                  setStreamFields((current) =>
                    current.map((item, itemIndex) =>
                      itemIndex === index
                        ? { ...item, value: event.target.value }
                        : item
                    )
                  )
                }
                placeholder="value"
                value={pair.value}
              />
              <Button
                aria-label={`Remove stream field ${index + 1}`}
                disabled={streamFields.length === 1}
                onClick={() =>
                  setStreamFields((current) =>
                    current.filter((_item, itemIndex) => itemIndex !== index)
                  )
                }
                size="icon"
                type="button"
                variant="ghost"
              >
                <Trash2 />
              </Button>
            </div>
          ))}
          <Button
            className="mt-2"
            onClick={() =>
              setStreamFields((current) => [
                ...current,
                { field: "", value: "" },
              ])
            }
            size="sm"
            type="button"
            variant="ghost"
          >
            <Plus />
            Field
          </Button>
        </div>
      ) : null}
      {type === "string" ||
      type === "hash" ||
      type === "list" ||
      type === "set" ||
      type === "zset" ? (
        <RedisBinaryInput
          encoding={valueEncoding}
          label={type === "set" || type === "zset" ? "Member" : "Value"}
          multiline={type === "string"}
          onEncodingChange={setValueEncoding}
          onValueChange={setValue}
          value={value}
        />
      ) : null}
      {type === "list" ? (
        <div className="mb-3 text-[9px] tracking-[0.1em] text-muted-foreground uppercase">
          <span>Push side</span>
          <Select
            items={{ left: "Left", right: "Right" }}
            onValueChange={(selected) =>
              setListSide(String(selected) as "left" | "right")
            }
            value={listSide}
          >
            <SelectTrigger className="mt-1.5 h-8 w-full text-xs tracking-normal normal-case">
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="start">
              <SelectItem value="left">Left</SelectItem>
              <SelectItem value="right">Right</SelectItem>
            </SelectContent>
          </Select>
        </div>
      ) : null}
      {type === "zset" ? (
        <label
          className="mb-3 block text-[9px] tracking-[0.1em] text-muted-foreground uppercase"
          htmlFor="redis-zset-score"
        >
          Score
          <Input
            className="mt-1.5"
            id="redis-zset-score"
            onChange={(event) => setScore(event.target.value)}
            required
            type="number"
            value={score}
          />
        </label>
      ) : null}
      {error ? (
        <p aria-live="polite" className="mb-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
      <div className="flex justify-end gap-2">
        <Button onClick={onCancel} type="button" variant="ghost">
          Cancel
        </Button>
        <Button disabled={busy} type="submit">
          Create key
        </Button>
      </div>
    </FormCard>
  );
};
