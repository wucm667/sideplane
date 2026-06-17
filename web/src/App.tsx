import FleetPage from './FleetPage.tsx'

function App() {
  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="border-b border-gray-200 bg-white px-6 py-4">
        <div className="mx-auto max-w-7xl">
          <h1 className="text-lg font-semibold tracking-tight text-gray-900">
            Sideplane
          </h1>
        </div>
      </header>
      <main className="mx-auto max-w-7xl px-6 py-6">
        <FleetPage />
      </main>
    </div>
  )
}

export default App
