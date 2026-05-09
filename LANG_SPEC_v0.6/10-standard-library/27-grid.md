### 10.27 Grid (`std.grid`)

`std.grid` contains small spatial types shared by text UIs, board games,
roguelikes, simulations, and pathfinding libraries.

Core types:

```osty
pub struct Point { pub x: Int, pub y: Int }
pub struct Size { pub width: Int, pub height: Int }
pub struct Rect { pub origin: Point, pub size: Size }
pub enum Direction { Up, Down, Left, Right, UpLeft, UpRight, DownLeft, DownRight, Wait }
pub struct Grid<T>
```

Constructors:

```osty
grid.point(x, y) -> Point
grid.size(width, height) -> Size
grid.rect(x, y, width, height) -> Rect
grid.grid<T>(width, height, fill) -> Grid<T>
grid.fromRows<T>(rows) -> Grid<T>?
```

`Grid<T>` is row-major. `Grid.index(point)` returns `None` outside bounds,
and all neighbor helpers filter out points outside the grid.

`std.grid` is deliberately not a pathfinding or roguelike engine. Higher
level packages can build A*, field of view, dungeon generation, and turn
scheduling on top of this stable geometry layer.

#### v0.6 purity

`std.grid` is *pure* — every type is value-semantic and every
operation transforms grid values without consulting any capability.
The whole module is acceptable inside `#[pure]` contexts.

The neighbor-iteration helpers (`Grid.neighbors(p)`,
`Grid.neighbors8(p)`) iterate in *deterministic* order — clockwise
from `Up` for the 4-direction set, and clockwise from `UpLeft` for
the 8-direction set. This deterministic order makes
`#[pure]` consumers stable across runs.

#### Information flow

`Grid<T>` cells inherit the element type's flow tag set. A
`Grid<#[taint("user_input")] String>` propagates the tag through
indexing, iteration, and `Grid.map` transformations.

#### Composition with rendering

A `Grid<Cell>` (where `Cell` is text+style) renders to a string via
`std.tui.Frame`:

```osty
fn render(grid: Grid<Cell>) -> String {
    let frame = tui.frame(grid.size())
    for p in grid.points() {
        let cell = grid.get(p).unwrap()
        frame.put(p, cell.text, cell.style)
    }
    frame.renderAnsi()
}
```

`render` is `#[pure]`-eligible (capability-free). Side-effecting
display happens via `Screen.present(frame)` (§10.26), which routes
through `Terminal` (capability).

#### 10.27.1 Grid coordinate determinism

`Point` and `Rect` operations are deterministic given their
inputs. Iteration helpers like `Grid.points()` traverse cells in
a fixed (row-major) order — first row top-to-bottom, then column
left-to-right within each row. This makes grid-based algorithms
(BFS, A*, flood fill) acceptable inside `#[pure]` contexts.

```osty
#[pure]
fn floodFill<T>(grid: Grid<T>, start: Point, target: T) -> Set<Point> {
    let mut visited: Set<Point> = Set.empty()
    let mut queue: List<Point> = [start]
    while !queue.isEmpty() {
        let p = queue.popFront()
        if visited.contains(p) { continue }
        if grid.get(p)? != target { continue }
        visited.insert(p)
        for n in grid.neighbors(p) {
            queue.push(n)
        }
    }
    visited.toListSorted()      // sorted for deterministic output
}
```

The final `toListSorted()` is critical for reproducibility — `Set`
iteration is hash-based and non-deterministic. `Set` *insertion
order* is irrelevant; only the final sorted projection is.

#### 10.27.2 Grid and information flow

A `Grid<T>` cell carries `T`'s flow tag set. `Grid.map(f)`
preserves tags through the transformation closure; `Grid.fill(t)`
produces an untagged grid (the fill value is the source).

For grid-of-text use cases (TUI map rendering), tainted text
flowing into cells preserves the tag through to the rendered
frame. Authors who want sanitization apply it in the cell
construction step:

```osty
fn buildTainted(input: #[taint("user_input")] String) -> Grid<Cell> {
    let escaped = std.term.escapeAnsi(input)
    let g = grid.grid::<Cell>(grid.size(80, 1), Cell.empty())
    g.set(grid.point(0, 0), Cell.text(escaped, styles.normal))
    g
}
```
