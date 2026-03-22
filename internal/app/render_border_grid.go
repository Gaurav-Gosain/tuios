package app

// Shared borders work by overlapping adjacent tiled windows by 1 cell
// in the BSP layout. The compositor renders higher-Z windows on top,
// naturally merging the border characters.
//
// Junction characters (┬, ┤, ├, ┴, ┼) would require either:
// 1. A post-compositing pass that strips ANSI to find border positions
// 2. A separate border grid overlay layer
// Both are complex. For now, the overlap produces visually acceptable
// results — │ overlaps │ correctly, and corner mismatches are minor.
//
// Future improvement: render a thin overlay layer at split positions
// with proper junction characters.
