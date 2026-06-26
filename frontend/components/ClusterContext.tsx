"use client";

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  ReactNode,
} from "react";

interface ClusterState {
  clusterId: string;
  namespace: string; // "" = all namespaces
  target: string; // upgrade target, "" = next minor
  setClusterId: (v: string) => void;
  setNamespace: (v: string) => void;
  setTarget: (v: string) => void;
}

const Ctx = createContext<ClusterState | null>(null);

const KEY = "kubepilot.context";

interface Persisted {
  clusterId: string;
  namespace: string;
  target: string;
}

const defaults: Persisted = { clusterId: "default", namespace: "", target: "" };

export function ClusterProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<Persisted>(defaults);

  useEffect(() => {
    try {
      const raw = localStorage.getItem(KEY);
      if (raw) setState({ ...defaults, ...JSON.parse(raw) });
    } catch {
      /* ignore */
    }
  }, []);

  useEffect(() => {
    try {
      localStorage.setItem(KEY, JSON.stringify(state));
    } catch {
      /* ignore */
    }
  }, [state]);

  const value = useMemo<ClusterState>(
    () => ({
      ...state,
      setClusterId: (clusterId) => setState((s) => ({ ...s, clusterId })),
      setNamespace: (namespace) => setState((s) => ({ ...s, namespace })),
      setTarget: (target) => setState((s) => ({ ...s, target })),
    }),
    [state],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useCluster(): ClusterState {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useCluster must be used within ClusterProvider");
  return ctx;
}
