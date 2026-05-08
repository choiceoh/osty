### 10.27 Grid (`std.grid`)

- **Scope**: Osty stdlib spec — 10.27 Grid (`std.grid`)
- **Type**: Standard library specification
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
