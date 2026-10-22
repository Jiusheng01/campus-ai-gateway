import {describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import {TrendChart} from './TrendChart'
import type {TrendBucket} from '../../api'

const points: TrendBucket[] = [
  {bucket: '2026-10-20 10:00', requests: 100, tokens: 1000, errors: 2, avg_latency_ms: 80, p95_latency_ms: 200},
  {bucket: '2026-10-20 11:00', requests: 0, tokens: 0, errors: 0, avg_latency_ms: 0, p95_latency_ms: 0},
  {bucket: '2026-10-20 12:00', requests: 50, tokens: 500, errors: 0, avg_latency_ms: 60, p95_latency_ms: 400},
]

function renderChart(over: Partial<React.ComponentProps<typeof TrendChart>> = {}) {
  return render(
    <TrendChart points={points} value={p => p.requests} format={n => String(n)} metric="请求数" hover={null} onHover={vi.fn()} {...over} />,
  )
}

describe('TrendChart aggregate readout', () => {
  // The series carries a P95 per bucket. Averaging those buckets would invent a
  // window figure the server never computed and that no percentile supports;
  // the slowest bucket is a real number and is labelled as such.
  it('does not present an average of bucket P95 values as the window P95', () => {
    renderChart()
    const readout = screen.getByRole('status')
    // (200 + 0 + 400) / 3 = 200 would be the averaged value.
    expect(readout).not.toHaveTextContent('200 ms')
    expect(readout).toHaveTextContent('最慢分桶 P95')
    expect(readout).toHaveTextContent('400 ms')
  })

  it('names an all-zero P95 as not measured rather than 0 ms', () => {
    renderChart({points: points.map(p => ({...p, p95_latency_ms: 0}))})
    const readout = screen.getByRole('status')
    expect(readout).toHaveTextContent('最慢分桶 P95')
    expect(readout).toHaveTextContent('—')
    expect(readout).not.toHaveTextContent('0 ms')
  })

  it('keeps a single bucket readout on that bucket', () => {
    renderChart({hover: 0})
    expect(screen.getByRole('status')).toHaveTextContent('P95 200 ms')
  })
})

describe('TrendChart own metric', () => {
  // Three charts share the readout shape; each has to say what its own bars
  // mean, or the panel reads as one chart printed three times.
  it('leads with the metric this chart draws', () => {
    renderChart({value: p => p.tokens, metric: 'Token 用量', format: n => String(n)})
    const readout = screen.getByRole('status')
    expect(readout).toHaveTextContent('Token 用量合计')
    expect(readout).toHaveTextContent('1500')
  })

  it('reports the hovered bucket value for its own metric', () => {
    renderChart({value: p => p.tokens, metric: 'Token 用量', format: n => String(n), hover: 2})
    expect(screen.getByRole('status')).toHaveTextContent('Token 用量 500')
  })
})

describe('TrendChart latency aggregate', () => {
  // Percentile-like values do not add up: 200 + 0 + 400 is not "600 ms of P95".
  it('reports the peak for a latency band rather than summing the buckets', () => {
    renderChart({value: p => p.p95_latency_ms, metric: 'P95 延迟', format: n => `${n} ms`})
    const readout = screen.getByRole('status')
    expect(readout).toHaveTextContent('P95 延迟峰值 400 ms')
    expect(readout).not.toHaveTextContent('600 ms')
  })
})

describe('TrendChart empty and unit facts', () => {
  it('says the range has no data instead of drawing an empty axis', () => {
    renderChart({points: []})
    expect(screen.getByText(/该时间范围内没有数据/)).toBeInTheDocument()
  })

  it('states the bar count and the peak with the metric unit', () => {
    renderChart()
    expect(screen.getByText(/峰值 100/)).toBeInTheDocument()
    expect(screen.getByText(/3 个时间点/)).toBeInTheDocument()
  })

  it('gives the plot an accessible name that names the metric', () => {
    renderChart()
    expect(screen.getByRole('img', {name: /请求数/})).toBeInTheDocument()
  })
})
