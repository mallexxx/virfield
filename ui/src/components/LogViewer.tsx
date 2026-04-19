import { useState, useEffect, useRef } from 'react';

type LogSource = 'app-console' | 'ui-tests' | 'peekaboo-mcp' | 'socat';

const SOURCES: LogSource[] = ['app-console', 'ui-tests', 'peekaboo-mcp', 'socat'];

const SOURCE_LABELS: Record<LogSource, string> = {
  'app-console': 'app-console',
  'ui-tests':    'tests',
  'peekaboo-mcp': 'peekaboo-mcp',
  'socat':       'socat',
};

interface LogLine {
  type: 'line' | 'error' | 'connected' | 'closed';
  data: string;
  ts: number;
}

interface Props {
  vmId: string;
}

export function LogViewer({ vmId }: Props) {
  const [source, setSource] = useState<LogSource>('app-console');
  const [lines, setLines] = useState<LogLine[]>([]);
  const [connected, setConnected] = useState(false);
  const [autoScroll, setAutoScroll] = useState(true);
  const [filter, setFilter] = useState('');
  const bottomRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    setLines([]);
    setConnected(false);

    const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${protocol}://${window.location.host}/api/${vmId}/logs/${source}/tail`);
    wsRef.current = ws;

    ws.onopen = () => setConnected(true);
    ws.onclose = () => setConnected(false);
    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data as string) as LogLine;
        setLines(prev => [...prev.slice(-2000), msg]);
      } catch { /* ignore */ }
    };

    return () => { ws.close(); };
  }, [vmId, source]);

  useEffect(() => {
    if (autoScroll) bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [lines, autoScroll]);

  const filtered = filter
    ? lines.filter(l => l.data.toLowerCase().includes(filter.toLowerCase()))
    : lines;

  return (
    <div className="flex flex-col h-80">
      {/* Toolbar */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-800 bg-gray-900/80 flex-shrink-0">
        <div className="flex gap-1">
          {SOURCES.map(s => (
            <button
              key={s}
              onClick={() => setSource(s)}
              className={`text-[10px] px-2 py-1 rounded border transition-colors ${
                source === s
                  ? 'bg-orange-500/20 text-orange-300 border-orange-500/40'
                  : 'bg-gray-800 text-gray-500 border-gray-700 hover:text-gray-300'
              }`}
            >
              {SOURCE_LABELS[s]}
            </button>
          ))}
        </div>
        <div className={`ml-2 w-2 h-2 rounded-full flex-shrink-0 ${connected ? 'bg-green-400' : 'bg-gray-600'}`} />
        <input
          type="text"
          placeholder="Filter..."
          value={filter}
          onChange={e => setFilter(e.target.value)}
          className="flex-1 ml-2 bg-gray-800 border border-gray-700 rounded px-2 py-0.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60"
        />
        <label className="flex items-center gap-1 text-[10px] text-gray-500 cursor-pointer">
          <input
            type="checkbox"
            checked={autoScroll}
            onChange={e => setAutoScroll(e.target.checked)}
            className="accent-orange-400"
          />
          auto-scroll
        </label>
        <button
          onClick={() => setLines([])}
          className="text-[10px] text-gray-600 hover:text-gray-400"
        >clear</button>
      </div>

      {/* Log content */}
      <div className="flex-1 overflow-y-auto bg-black/60 p-3 font-mono text-[11px]">
        {filtered.length === 0 && (
          <div className="text-gray-600 text-center mt-8">
            {connected ? 'Waiting for log data...' : 'Connecting...'}
          </div>
        )}
        {filtered.map((line, i) => (
          <div
            key={i}
            className={`leading-5 ${
              line.type === 'error' ? 'text-red-400' :
              line.type === 'connected' ? 'text-green-500' :
              line.type === 'closed' ? 'text-yellow-500' :
              'text-gray-300'
            }`}
          >
            {line.data}
          </div>
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}
