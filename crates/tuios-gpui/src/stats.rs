//! Frame timing, kept in a ring so the status bar and the performance
//! harness can report percentiles.

use std::time::Duration;

const KEEP: usize = 600;

#[derive(Default)]
pub struct FrameStats {
    paints: Vec<f64>,
    next: usize,
}

impl FrameStats {
    pub fn record_paint(&mut self, d: Duration) {
        let ms = d.as_secs_f64() * 1000.;
        if self.paints.len() < KEEP {
            self.paints.push(ms);
        } else {
            self.paints[self.next] = ms;
        }
        self.next = (self.next + 1) % KEEP;
    }

    pub fn paint_percentiles(&self) -> Option<(f64, f64)> {
        Some((percentile(&self.paints, 50.)?, percentile(&self.paints, 95.)?))
    }

    pub fn clear(&mut self) {
        self.paints.clear();
        self.next = 0;
    }
}

/// The p-th percentile by nearest rank. None for no samples.
pub fn percentile(samples: &[f64], p: f64) -> Option<f64> {
    if samples.is_empty() {
        return None;
    }
    let mut v = samples.to_vec();
    v.sort_by(|a, b| a.total_cmp(b));
    let rank = ((p / 100.) * v.len() as f64).ceil().max(1.) as usize;
    Some(v[rank.min(v.len()) - 1])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn nearest_rank() {
        let v: Vec<f64> = (1..=100).map(|i| i as f64).collect();
        assert_eq!(percentile(&v, 50.), Some(50.));
        assert_eq!(percentile(&v, 95.), Some(95.));
        assert_eq!(percentile(&[3.], 95.), Some(3.));
        assert_eq!(percentile(&[], 50.), None);
    }
}
