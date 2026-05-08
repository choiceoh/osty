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

#### v0.6 reproducibility

`std.grid` is *pure* — every type is value-semantic and every
operation transforms grid values without consulting any capability.
The whole module is acceptable inside `#[reproducible(scope =
"portable")]` and `#[pure]` contexts.

The neighbor-iteration helpers (`Grid.neighbors(p)`,
`Grid.neighbors8(p)`) iterate in *deterministic* order — clockwise
from `Up` for the 4-direction set, and clockwise from `UpLeft` for
the 8-direction set. This deterministic order makes
`#[reproducible]` consumers stable across runs.

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
