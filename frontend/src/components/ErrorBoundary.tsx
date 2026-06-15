import { Component, type ReactNode } from 'react'
import { AlertTriangle, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface Props { children: ReactNode }
interface State { error: Error | null }

// Catches render errors so one broken component (e.g. a page hitting undefined
// data mid-backend-restart) shows a recoverable fallback instead of a blank
// white screen for the whole app.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error) {
    console.error('Render error caught by ErrorBoundary:', error)
  }

  render() {
    if (this.state.error) {
      return (
        <div className="min-h-screen flex items-center justify-center p-6">
          <div className="max-w-md w-full text-center space-y-4">
            <div className="w-12 h-12 rounded-full bg-destructive/10 flex items-center justify-center mx-auto">
              <AlertTriangle className="h-6 w-6 text-destructive" />
            </div>
            <div className="space-y-1">
              <h1 className="text-lg font-semibold">Something went wrong</h1>
              <p className="text-sm text-muted-foreground break-words">{this.state.error.message}</p>
            </div>
            <Button onClick={() => this.setState({ error: null })} variant="outline" className="gap-2">
              <RefreshCw className="h-4 w-4" /> Try again
            </Button>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}
