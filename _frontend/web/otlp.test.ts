import { describe, expect, test } from "bun:test";

import { otlpAttributeMap, otlpNativeValue, otlpTextAttributes } from "./otlp";

describe("OTLP values", () => {
  test("decodes compound values and object attribute maps consistently", () => {
    expect(
      otlpNativeValue({
        kvlistValue: {
          values: [
            {
              key: "nested",
              value: {
                arrayValue: {
                  values: [{ intValue: "42" }, { boolValue: true }],
                },
              },
            },
          ],
        },
      })
    ).toEqual({ nested: ["42", true] });
    expect(otlpNativeValue({ bytesValue: "AQI=" })).toBe("AQI=");

    const mapped = otlpAttributeMap({
      attributes: {
        count: { intValue: "42" },
        values: { arrayValue: { values: [{ stringValue: "a" }] } },
      },
    });
    expect(mapped.get("count")).toBe("42");
    expect(mapped.get("values")).toEqual(["a"]);
    expect(
      otlpTextAttributes({ attributes: { values: mapped.get("values") } })
    ).toEqual([{ key: "values", value: '["a"]' }]);
  });
});
