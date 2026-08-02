import { variableReferences } from "@/variable-expression";

export interface ProjectApplyOperation {
  buildEnvironment?: Readonly<Record<string, string>>;
  environment?: Readonly<Record<string, string>>;
  id: string;
  label: string;
  resourceName: string;
  run: () => Promise<unknown>;
}

export interface ProjectApplyOutcome<
  Operation extends ProjectApplyOperation = ProjectApplyOperation,
> {
  operation: Operation;
  reason?: unknown;
  status: "blocked" | "fulfilled" | "rejected";
}

interface PlannedOperation<
  Operation extends ProjectApplyOperation = ProjectApplyOperation,
> {
  dependencies: string[];
  operation: Operation;
}

const projectApplyWaves = <Operation extends ProjectApplyOperation>(
  operations: readonly Operation[],
  existingResourceNames: ReadonlySet<string>
): PlannedOperation<Operation>[][] => {
  const operationsByID = new Map<string, Operation>();
  const operationsByName = new Map<string, Operation>();
  for (const operation of operations) {
    if (operationsByID.has(operation.id)) {
      throw new Error(`Duplicate pending operation ${operation.id}`);
    }
    if (operationsByName.has(operation.resourceName)) {
      throw new Error(
        `Multiple pending operations target ${operation.resourceName}`
      );
    }
    operationsByID.set(operation.id, operation);
    operationsByName.set(operation.resourceName, operation);
  }

  const knownResourceNames = new Set([
    ...existingResourceNames,
    ...operationsByName.keys(),
  ]);
  const dependencies = new Map<string, Set<string>>();
  const dependents = new Map<string, Set<string>>();
  for (const operation of operations) {
    const operationDependencies = new Set<string>();
    for (const environment of [
      operation.environment,
      operation.buildEnvironment,
    ]) {
      for (const value of Object.values(environment ?? {})) {
        for (const reference of variableReferences(value)) {
          if (!knownResourceNames.has(reference.resource)) {
            throw new Error(
              `${operation.label} references missing resource ${reference.resource}`
            );
          }
          const dependency = operationsByName.get(reference.resource);
          if (!(dependency && dependency.id !== operation.id)) {
            continue;
          }
          operationDependencies.add(dependency.id);
          const dependencyDependents =
            dependents.get(dependency.id) ?? new Set();
          dependencyDependents.add(operation.id);
          dependents.set(dependency.id, dependencyDependents);
        }
      }
    }
    dependencies.set(operation.id, operationDependencies);
  }

  const remainingDependencies = new Map(
    operations.map((operation) => [
      operation.id,
      dependencies.get(operation.id)?.size ?? 0,
    ])
  );
  let ready = operations.filter(
    (operation) => remainingDependencies.get(operation.id) === 0
  );
  const waves: PlannedOperation<Operation>[][] = [];
  let plannedCount = 0;
  while (ready.length) {
    waves.push(
      ready.map((operation) => ({
        dependencies: [...(dependencies.get(operation.id) ?? [])],
        operation,
      }))
    );
    plannedCount += ready.length;
    const nextReadyIDs = new Set<string>();
    for (const operation of ready) {
      for (const dependentID of dependents.get(operation.id) ?? []) {
        const remaining = (remainingDependencies.get(dependentID) ?? 0) - 1;
        remainingDependencies.set(dependentID, remaining);
        if (remaining === 0) {
          nextReadyIDs.add(dependentID);
        }
      }
    }
    ready = operations.filter((operation) => nextReadyIDs.has(operation.id));
  }
  if (plannedCount !== operations.length) {
    const affected = operations
      .filter((operation) => (remainingDependencies.get(operation.id) ?? 0) > 0)
      .map((operation) => operation.resourceName)
      .toSorted();
    throw new Error(
      `Variable reference cycle affects pending resources: ${affected.join(", ")}`
    );
  }
  return waves;
};

export const applyProjectOperations = async <
  Operation extends ProjectApplyOperation,
>(
  operations: readonly Operation[],
  existingResourceNames: ReadonlySet<string>
): Promise<ProjectApplyOutcome<Operation>[]> => {
  const waves = projectApplyWaves(operations, existingResourceNames);
  const outcomes: ProjectApplyOutcome<Operation>[] = [];
  const unsuccessful = new Set<string>();
  const applyWave = async (index: number): Promise<void> => {
    const wave = waves[index];
    if (!wave) {
      return;
    }
    const blockedIDs = new Set(
      wave
        .filter((planned) =>
          planned.dependencies.some((dependency) =>
            unsuccessful.has(dependency)
          )
        )
        .map((planned) => planned.operation.id)
    );
    const runnable = wave.filter(
      (planned) => !blockedIDs.has(planned.operation.id)
    );
    for (const blockedID of blockedIDs) {
      unsuccessful.add(blockedID);
    }
    const settled = await Promise.allSettled(
      runnable.map(({ operation }) => Promise.resolve().then(operation.run))
    );
    const settledByID = new Map(
      runnable.map((planned, settledIndex) => [
        planned.operation.id,
        settled[settledIndex],
      ])
    );
    for (const planned of wave) {
      if (blockedIDs.has(planned.operation.id)) {
        outcomes.push({ operation: planned.operation, status: "blocked" });
        continue;
      }
      const result = settledByID.get(planned.operation.id);
      if (result?.status === "fulfilled") {
        outcomes.push({ operation: planned.operation, status: "fulfilled" });
        continue;
      }
      unsuccessful.add(planned.operation.id);
      outcomes.push({
        operation: planned.operation,
        reason: result?.reason,
        status: "rejected",
      });
    }
    await applyWave(index + 1);
  };
  await applyWave(0);
  return outcomes;
};
